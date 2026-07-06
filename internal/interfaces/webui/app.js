const $=s=>document.querySelector(s), api=(p,q)=>fetch("/api/"+p+"?"+new URLSearchParams(q)).then(r=>r.json());
const esc=s=>(s||"").replace(/[&<>]/g,c=>({"&":"&amp;","<":"&lt;",">":"&gt;"}[c]));
let REF="HEAD", REFS=[];

function tag(k){return '<span class="tag '+k+'">'+k+'</span>';}
function refSel(el,val){el.innerHTML=REFS.map(r=>'<option '+(r===val?'selected':'')+'>'+esc(r)+'</option>').join('');}

async function boot(){
  const d=await api("refs",{}); $("#repo").textContent=d.repo||""; REFS=d.refs||[];
  REF=REFS.includes("HEAD")?"HEAD":(REFS[0]||"HEAD");
  refSel($("#ref"),REF); refSel($("#from"),REFS[REFS.length-1]||REF); refSel($("#to"),REF);
  $("#ref").onchange=e=>{REF=e.target.value;search();};
}
document.querySelectorAll("nav button").forEach(b=>b.onclick=()=>{
  document.querySelectorAll("nav button").forEach(x=>x.classList.toggle("on",x===b));
  const v=b.dataset.v;
  ["explore","diff","impact"].forEach(n=>{$("#"+n).classList.toggle("on",n===v);
    $("#"+n+"-side").style.display=n===v?"":"none";});
});

// Explore: search -> brief
async function search(){
  const q=$("#q").value.trim(); if(!q){$("#hits").innerHTML="";return;}
  const hits=await api("search",{q,ref:REF});
  $("#hits").innerHTML=(hits||[]).map(h=>'<div class="row" onclick="brief(\''+esc(h.name)+'\')">'+esc(h.name)+(h.is_test?' <span class="pill">test</span>':'')+'</div>').join('')||'<div class="empty">no matches</div>';
}
$("#q").oninput=()=>{clearTimeout(window._t);window._t=setTimeout(search,180);};

async function brief(sym){
  const b=await api("brief",{symbol:sym,ref:REF});
  const t=b.target||{};
  let h='<h2>'+esc(b.symbol)+(t.exported?' <span class="pill">exported</span>':'')+'</h2>';
  if(t.path)h+='<div class="k mono">'+esc(t.path)+':'+(t.start_line||'')+'</div>';
  if(t.body)h+='<pre>'+esc(t.body)+'</pre>';
  const nb=(arr,title)=>arr&&arr.length?'<h2>'+title+'</h2>'+arr.map(n=>'<div class="row" onclick="brief(\''+esc(n.handle||n.name)+'\')">'+esc(n.name)+(n.resolution_method?' <span class="pill">'+esc(n.resolution_method)+'</span>':'')+'</div>').join(''):'';
  h+=nb(b.callees,"Callees");h+=nb(b.callers,"Callers (blast radius)");
  if(b.tests&&b.tests.length)h+='<h2>Covering tests</h2>'+b.tests.map(x=>'<div class="row mono">'+esc(x)+'</div>').join('');
  if(b.compat&&b.compat.length)h+='<h2>Compat across refs</h2>'+b.compat.map(c=>'<div class="row">'+tag(c.verdict)+esc(c.ref)+' <span class="pill">conf '+c.confidence+'</span></div>').join('');
  if(b.intent)h+='<h2>Why</h2><pre>'+esc(JSON.stringify(b.intent,null,2))+'</pre>';
  $("#explore").innerHTML=h;
}

// Diff
async function diff(){
  const from=$("#from").value,to=$("#to").value;
  const d=await api("diff",{from,to,changed_only:$("#changed").checked});
  const c=d.changes||[];
  $("#changes").innerHTML='<span class="pill">'+c.length+' symbols</span>';
  $("#diff").innerHTML='<h2>'+esc(from)+' → '+esc(to)+'</h2>'+(c.length?c.map(x=>'<div class="row" onclick="brief(\''+esc(x.name)+'\')">'+tag(x.change)+esc(x.name)+'</div>').join(''):'<div class="empty">no differences</div>');
}
$("#from").onchange=diff;$("#to").onchange=diff;$("#changed").onchange=diff;

// Impact — module-resolved callers (precise) are marked; name-based are the fallback tier.
async function impact(){
  const s=$("#xq").value.trim(); if(!s){$("#impact").innerHTML='<div class="empty">Enter a symbol.</div>';return;}
  const d=await api("ximpact",{symbol:s});
  const c=d.callers||[];
  const mods=(d.modules||[]).length?'<p class="k" style="font-size:12px">module '+esc((d.modules||[]).join(", "))+'</p>':'';
  const line=x=>{
    const precise=x.resolution_method==="import-resolved";
    const badge=' <span class="pill'+(precise?' precise':'')+'">'+esc(x.resolution_method||"")+'</span>';
    const mod=x.module?' <span class="k mono">'+esc(x.module)+'</span>':'';
    return '<div class="row">'+esc(x.repo)+' <span class="k">@'+esc(x.ref)+'</span> → '+esc(x.caller)+mod+badge+' <span class="pill">conf '+x.confidence+'</span></div>';
  };
  $("#impact").innerHTML='<h2>Callers of '+esc(s)+'</h2>'+mods+(c.length?c.map(line).join(''):'<div class="empty">no external callers found</div>')+'<p class="k" style="font-size:11.5px;margin-top:14px">'+esc(d.note||"")+'</p>';
}
$("#xq").oninput=()=>{clearTimeout(window._x);window._x=setTimeout(impact,220);};

boot();
