"use strict";
// reponite dashboard — graph canvas. Dependency-free SVG: layered layout for
// neighborhoods (callers ← focus → callees), force layout for maps; pan with
// drag, zoom with the wheel, drag a node in a force graph, hover highlights a
// node's edges, click selects, double-click refocuses.
(function(){
const NS="http://www.w3.org/2000/svg";
const el=(t,a)=>{const e=document.createElementNS(NS,t);for(const k in a||{})e.setAttribute(k,a[k]);return e;};
const hue=s=>{let h=0;for(const c of String(s))h=(h*31+c.charCodeAt(0))>>>0;return h%360;};

class GraphCanvas{
  constructor(host,opts){
    this.host=host;this.opts=opts||{};
    host.innerHTML="";host.classList.add("gcanvas");
    this.svg=el("svg",{width:"100%",height:"100%"});
    const defs=el("defs");
    for(const [id,cls] of [["arr","edge"],["arrhi","edge hi"]]){
      const m=el("marker",{id:"g-"+id,viewBox:"0 0 10 10",refX:"9",refY:"5",markerWidth:"7",markerHeight:"7",orient:"auto-start-reverse"});
      const p=el("path",{d:"M0,0 L10,5 L0,10 z",class:"arrow "+cls});m.appendChild(p);defs.appendChild(m);
    }
    this.svg.appendChild(defs);
    this.g=el("g");this.svg.appendChild(this.g);
    this.eg=el("g",{class:"edges"});this.ng=el("g",{class:"nodes"});this.g.appendChild(this.eg);this.g.appendChild(this.ng);
    host.appendChild(this.svg);
    this.tf={x:0,y:0,k:1};this._pan();
  }
  _apply(){this.g.setAttribute("transform",`translate(${this.tf.x},${this.tf.y}) scale(${this.tf.k})`);}
  _pan(){
    let drag=null;
    this.svg.addEventListener("mousedown",e=>{if(e.target.closest(".gnode"))return;drag={x:e.clientX,y:e.clientY,tx:this.tf.x,ty:this.tf.y};this.svg.classList.add("panning");});
    addEventListener("mousemove",e=>{if(!drag)return;this.tf.x=drag.tx+e.clientX-drag.x;this.tf.y=drag.ty+e.clientY-drag.y;this._apply();});
    addEventListener("mouseup",()=>{drag=null;this.svg.classList.remove("panning");});
    this.svg.addEventListener("wheel",e=>{e.preventDefault();const r=this.svg.getBoundingClientRect();
      const mx=e.clientX-r.left,my=e.clientY-r.top,f=Math.exp(-e.deltaY*0.0015),k=Math.min(4,Math.max(0.15,this.tf.k*f));
      this.tf.x=mx-(mx-this.tf.x)*(k/this.tf.k);this.tf.y=my-(my-this.tf.y)*(k/this.tf.k);this.tf.k=k;this._apply();},{passive:false});
  }
  // fit the drawing, but never below a readable scale: a big neighborhood is
  // then centred on its focus and explored by panning
  fit(){
    const b=this.g.getBBox(),r=this.svg.getBoundingClientRect();if(!b.width||!r.width)return;
    const k=Math.min(1.3,Math.max(this.opts.minScale||0.55,Math.min((r.width-40)/b.width,(r.height-40)/b.height)));
    const f=this.nodes.find(n=>n.cls&&n.cls.includes("focus"));
    const cx=k*b.width<=r.width||!f?b.x+b.width/2:f.x,cy=k*b.height<=r.height||!f?b.y+b.height/2:f.y;
    this.tf={k,x:r.width/2-cx*k,y:r.height/2-cy*k};this._apply();
  }
  // nodes: {id,label,sub,color,col?,size?,shape?,faded?}; edges: {from,to,width?,dashed?,label?,color?}
  render(nodes,edges,layout){
    this.nodes=nodes;this.edges=edges;this.byId=new Map(nodes.map(n=>[n.id,n]));
    if(layout==="layered")this._layered();else this._force();
    this._draw();this.fit();
  }
  _layered(){
    const cols=new Map();
    for(const n of this.nodes){const c=n.col||0;if(!cols.has(c))cols.set(c,[]);cols.get(c).push(n);}
    const colW=this.opts.colW||300,rowH=this.opts.rowH||46;
    for(const [c,ns] of cols){
      ns.sort((a,b)=>(a.sub||"").localeCompare(b.sub||"")||a.label.localeCompare(b.label));
      ns.forEach((n,i)=>{n.x=c*colW;n.y=(i-(ns.length-1)/2)*rowH;});
    }
  }
  _force(){
    const N=this.nodes,n=N.length,R=Math.max(200,Math.sqrt(n)*90);
    N.forEach((p,i)=>{if(p.x==null){const a=i/n*2*Math.PI;p.x=R*Math.cos(a);p.y=R*Math.sin(a);}p.vx=0;p.vy=0;});
    const k=Math.max(this.opts.spacing||70,Math.sqrt((4*R*R)/Math.max(1,n)));
    const E=this.edges.map(e=>[this.byId.get(e.from),this.byId.get(e.to)]).filter(([a,b])=>a&&b&&a!==b);
    for(let it=0,t=R/4;it<420;it++,t*=0.988){
      for(let i=0;i<n;i++){const a=N[i];a.dx=-a.x*0.012;a.dy=-a.y*0.012;}
      for(let i=0;i<n;i++)for(let j=i+1;j<n;j++){const a=N[i],b=N[j];let dx=a.x-b.x,dy=a.y-b.y,d2=dx*dx+dy*dy+0.01;
        const f=k*k/d2;a.dx+=dx*f;a.dy+=dy*f;b.dx-=dx*f;b.dy-=dy*f;}
      for(const [a,b] of E){const dx=a.x-b.x,dy=a.y-b.y,d=Math.sqrt(dx*dx+dy*dy)+0.01,f=d/k;
        a.dx-=dx*f*0.5;a.dy-=dy*f*0.5;b.dx+=dx*f*0.5;b.dy+=dy*f*0.5;}
      for(const a of N){if(a.pinned)continue;const d=Math.sqrt(a.dx*a.dx+a.dy*a.dy)+0.01,m=Math.min(d,t);a.x+=a.dx/d*m;a.y+=a.dy/d*m;}
    }
  }
  _edgePath(e){
    const a=this.byId.get(e.from),b=this.byId.get(e.to);if(!a||!b)return "";
    const ra=a.w?a.w/2:a.r||8,rb=b.w?b.w/2:b.r||8;
    if(this.opts.layered){const x1=a.x+ra,x2=b.x-rb,mx=(x1+x2)/2;
      if(x2<x1){const yb=Math.max(a.y,b.y)+60;return `M${a.x},${a.y+12} C${a.x},${yb} ${b.x},${yb} ${b.x},${b.y+12}`;}
      return `M${x1},${a.y} C${mx},${a.y} ${mx},${b.y} ${x2},${b.y}`;}
    const dx=b.x-a.x,dy=b.y-a.y,d=Math.sqrt(dx*dx+dy*dy)||1,ux=dx/d,uy=dy/d;
    return `M${a.x+ux*(a.r||10)},${a.y+uy*(a.r||10)} L${b.x-ux*((b.r||10)+3)},${b.y-uy*((b.r||10)+3)}`;
  }
  _draw(){
    this.eg.innerHTML="";this.ng.innerHTML="";
    // nodes first, so each box is sized to its measured label before edges attach to it
    this.nodeEls=this.nodes.map(n=>{
      const g=el("g",{class:"gnode"+(n.cls?" "+n.cls:""),transform:`translate(${n.x},${n.y})`});
      this.ng.appendChild(g);
      if(n.shape==="circle"){n.r=n.size||10;
        const c=el("circle",{r:n.r});c.style.fill=n.color||"var(--surface)";g.appendChild(c);
        const t=el("text",{y:n.r+13,"text-anchor":"middle",class:"glabel"});t.textContent=n.label;g.appendChild(t);
      }else{
        const t=el("text",{x:0,y:4,"text-anchor":"middle",class:"glabel"});t.textContent=n.label;g.appendChild(t);
        let tw=0;try{tw=t.getComputedTextLength();}catch(e){}
        const w=Math.max(70,(tw||n.label.length*7.6)+26);n.w=w;
        const r=el("rect",{x:-w/2,y:-15,width:w,height:30,rx:8});if(n.color)r.style.stroke=n.color;
        g.insertBefore(r,t);
        if(n.sub){const s=el("text",{x:0,y:-20,"text-anchor":"middle",class:"gsub"});s.textContent=n.sub.length>48?"…"+n.sub.slice(-47):n.sub;g.appendChild(s);}
        if(n.badge){const bt=el("text",{x:w/2-4,y:-18,"text-anchor":"end",class:"gbadge"});bt.textContent=n.badge;g.appendChild(bt);}
      }
      const tt=el("title");tt.textContent=n.title||n.id;g.appendChild(tt);
      g.addEventListener("mouseenter",()=>this._hi(n.id,true));g.addEventListener("mouseleave",()=>this._hi(n.id,false));
      g.addEventListener("click",ev=>{ev.stopPropagation();this.select(n.id);this.opts.onSelect&&this.opts.onSelect(n);});
      g.addEventListener("dblclick",ev=>{ev.stopPropagation();this.opts.onOpen&&this.opts.onOpen(n);});
      if(!this.opts.layered)this._dragNode(g,n);
      return {n,g};});
    if(this.opts.layered)this._spreadColumns();
    this.edgeEls=this.edges.map(e=>{
      const p=el("path",{d:this._edgePath(e),class:"gedge"+(e.dashed?" dashed":""),"marker-end":this.opts.arrows===false?"":"url(#g-arr)"});
      p.style.strokeWidth=e.width||1.4;if(e.color)p.style.stroke=e.color;
      if(e.title){const t=el("title");t.textContent=e.title;p.appendChild(t);}
      this.eg.appendChild(p);return {e,p};});
  }
  // columns as wide as their widest box, with a fixed gap between them
  _spreadColumns(){
    const cols=[...new Set(this.nodes.map(n=>n.col||0))].sort((a,b)=>a-b),width={};
    for(const n of this.nodes){const c=n.col||0;width[c]=Math.max(width[c]||0,n.w||80);}
    const gap=this.opts.gap||110,x={};let cur=0;
    cols.forEach((c,i)=>{if(i)cur+=width[cols[i-1]]/2+gap+width[c]/2;x[c]=cur;});
    const z=x[0]||0;
    for(const {n,g} of this.nodeEls){n.x=x[n.col||0]-z;g.setAttribute("transform",`translate(${n.x},${n.y})`);}
  }
  _dragNode(g,n){
    let d=null;
    g.addEventListener("mousedown",e=>{e.stopPropagation();d={x:e.clientX,y:e.clientY,nx:n.x,ny:n.y};});
    addEventListener("mousemove",e=>{if(!d)return;n.x=d.nx+(e.clientX-d.x)/this.tf.k;n.y=d.ny+(e.clientY-d.y)/this.tf.k;n.pinned=true;
      g.setAttribute("transform",`translate(${n.x},${n.y})`);
      for(const {e:ed,p} of this.edgeEls)if(ed.from===n.id||ed.to===n.id)p.setAttribute("d",this._edgePath(ed));});
    addEventListener("mouseup",()=>{d=null;});
  }
  _hi(id,on){
    this.svg.classList.toggle("hovering",on);
    const near=new Set([id]);
    for(const {e,p} of this.edgeEls){const hit=on&&(e.from===id||e.to===id);p.classList.toggle("hi",hit);
      if(hit)p.setAttribute("marker-end",this.opts.arrows===false?"":"url(#g-arrhi)");else p.setAttribute("marker-end",this.opts.arrows===false?"":"url(#g-arr)");
      if(hit){near.add(e.from);near.add(e.to);}}
    for(const {n,g} of this.nodeEls)g.classList.toggle("near",on&&near.has(n.id));
  }
  select(id){for(const {n,g} of this.nodeEls)g.classList.toggle("sel",n.id===id);}
}
window.GraphCanvas=GraphCanvas;window.graphHue=hue;
})();
