"use strict";
// reponite dashboard — vanilla JS SPA. Hash-routed + deep-linkable, state
// persisted to localStorage, with loading / empty / error states throughout.
const $=s=>document.querySelector(s), $$=s=>[...document.querySelectorAll(s)];
const esc=s=>String(s==null?"":s).replace(/[&<>"]/g,c=>({"&":"&amp;","<":"&lt;",">":"&gt;",'"':"&quot;"}[c]));
const api=(p,q)=>fetch("/api/"+p+"?"+new URLSearchParams(q)).then(r=>{if(!r.ok)throw new Error(p+": "+r.status);return r.json();});
const num=n=>(n==null?"–":n.toLocaleString());
const LS=(k,v)=>v===undefined?localStorage.getItem("reponite."+k):localStorage.setItem("reponite."+k,v);

const S={repo:"",ref:"HEAD",repos:[],refs:[],overview:null};
// Fleet-wide views read every repo at HEAD, which a server started with
// --refs / REPONITE_REFS maps to each repo's pinned version.
const FLEET_REF="HEAD";

/* ---------- toast ---------- */
function toast(msg){const t=document.createElement("div");t.className="toast";t.textContent=msg;
  $("#toast").appendChild(t);setTimeout(()=>t.remove(),5000);}

/* ---------- theme ---------- */
function initTheme(){const t=LS("theme");if(t)document.documentElement.dataset.theme=t;}
$("#theme").onclick=()=>{const cur=document.documentElement.dataset.theme
    ||(matchMedia("(prefers-color-scheme:dark)").matches?"dark":"light");
  const next=cur==="dark"?"light":"dark";document.documentElement.dataset.theme=next;LS("theme",next);};

/* ---------- state helpers ---------- */
function setChip(){const ov=repoOverview();const rs=ov&&ov.refs.find(r=>r.ref===S.ref);
  $("#idx-chip").innerHTML=rs?`<b>${num(rs.symbols)}</b> symbols · <b>${num(rs.edges)}</b> edges · <b>${num(ov.refs.length)}</b> refs`
    :`<b>${S.refs.length}</b> refs`;}
function repoOverview(){return S.overview&&S.overview.find(o=>o.repo===S.repo);}
function fillSel(sel,opts,val){sel.innerHTML=opts.map(o=>`<option ${o===val?"selected":""}>${esc(o)}</option>`).join("");}

async function boot(){
  initTheme();
  try{
    const r=await api("repos",{}); S.repos=r.repos||[];
    S.repo=S.repos.includes(LS("repo"))?LS("repo"):(S.repos[0]||"");
    if(S.repos.length>1){$("#repo-sel-wrap").hidden=false;fillSel($("#repo"),S.repos,S.repo);
      $("#repo").onchange=e=>{S.repo=e.target.value;LS("repo",S.repo);loadRefs().then(route);};}
    S.overview=(await api("overview",{})).repos||[];
    if(!S.repos.includes(LS("repo"))&&S.overview.length){ // default to the largest repo, not the first by name
      const size=o=>Math.max(0,...(o.refs||[]).map(r=>r.symbols||0));
      S.repo=S.overview.slice().sort((a,b)=>size(b)-size(a))[0].repo;if(S.repos.length>1)fillSel($("#repo"),S.repos,S.repo);}
    await loadRefs();
    route();
  }catch(e){toast("Failed to load index: "+e.message);}
}
async function loadRefs(){
  const d=await api("refs",{repo:S.repo}); S.refs=d.refs||[];
  S.ref=S.refs.includes(LS("ref"))?LS("ref"):(S.refs.includes("HEAD")?"HEAD":(S.refs[0]||"HEAD"));
  fillSel($("#ref"),S.refs,S.ref); fillSel($("#from"),S.refs,S.refs[S.refs.length-1]); fillSel($("#to"),S.refs,S.ref);
  setChip();
}
$("#ref").onchange=e=>{S.ref=e.target.value;LS("ref",S.ref);setChip();route();};

/* ---------- router ---------- */
function parseHash(){const h=location.hash.replace(/^#\/?/,"")||"overview";const [view,qs]=h.split("?");
  return {view:view||"overview",params:Object.fromEntries(new URLSearchParams(qs||""))};}
function go(view,params){const qs=new URLSearchParams(params||{}).toString();
  location.hash="#/"+view+(qs?"?"+qs:"");}
function setParam(k,v){const {view,params}=parseHash();if(v)params[k]=v;else delete params[k];
  history.replaceState(null,"",location.pathname+"#/"+view+"?"+new URLSearchParams(params));}

const VIEWS={overview:renderOverview,explore:renderExplore,graph:renderGraph,fleet:renderFleet,links:renderLinks,
  diff:renderDiff,impact:renderImpact,topics:renderTopics,usages:renderUsages,coverage:renderCoverage,verify:renderVerify};
function route(){const {view,params}=parseHash();const v=VIEWS[view]?view:"overview";
  $$(".view").forEach(el=>el.classList.toggle("on",el.id===v));
  $$("nav.rail a").forEach(a=>a.classList.toggle("on",a.dataset.view===v));
  VIEWS[v](params);}
addEventListener("hashchange",route);

/* ---------- overview / database ---------- */
function renderOverview(){
  const body=$("#overview-body");
  body.innerHTML=`<div class="h-eyebrow">Index</div><div class="page-h">Overview</div>
    <div class="page-sub">What reponite has indexed — logical stats per ref and the physical database behind them.</div>`;
  const list=S.overview||[];
  if(!list.length){body.insertAdjacentHTML("beforeend",emptyState("nothing indexed","Run <span class='mono'>reponite index .</span> in a repo, then reload."));return;}
  for(const o of list) body.insertAdjacentHTML("beforeend",repoCard(o));
  $$(".copybtn").forEach(b=>b.onclick=()=>{navigator.clipboard&&navigator.clipboard.writeText(b.dataset.p);b.textContent="copied";setTimeout(()=>b.textContent="copy",1200);});
}
function repoCard(o){
  const head=o.refs.find(r=>r.ref===S.ref)||o.refs.find(r=>r.ref==="HEAD")||o.refs[0]||{};
  const tiles=[["Symbols",head.symbols],["Call edges",head.edges],["Files",head.files],["Refs",o.refs.length]]
    .map(([l,n])=>`<div class="tile"><div class="n">${num(n)}</div><div class="l">${l}</div></div>`).join("");
  const max=Math.max(1,...(o.tables||[]).map(t=>t.rows));
  const meters=(o.tables||[]).map(t=>`<div class="meter"><span class="mn">${esc(t.name)}</span>
    <span class="track"><span class="fill" style="width:${Math.max(2,Math.round(t.rows/max*100))}%"></span></span>
    <span class="mv">${num(t.rows)}</span></div>`).join("");
  const rows=o.refs.map(r=>`<tr><td class="name">${esc(r.ref)}</td><td class="name" style="color:var(--faint)">${esc((r.commit||"").slice(0,10))}</td>
    <td class="mono">${num(r.symbols)}</td><td class="mono">${num(r.edges)}</td><td class="mono">${num(r.files)}</td></tr>`).join("");
  const dbpath=o.db_path?`<div class="pathrow"><span class="lbl">db</span><span class="p" title="${esc(o.db_path)}">${esc(o.db_path)}</span>
    <button class="copybtn" data-p="${esc(o.db_path)}">copy</button></div>`:"";
  return `<div class="card">
    <div class="card-head"><h3>${esc(o.repo)}</h3>${o.module?`<span class="badge">${esc(o.module)}</span>`:`<span class="pill">name-based ximpact</span>`}</div>
    ${dbpath}
    <div class="tiles">${tiles}</div>
    ${meters?`<h2 class="sec">Database — rows per table</h2>${meters}`:""}
    <h2 class="sec">Refs</h2>
    <table class="grid"><thead><tr><th>Ref</th><th>Commit</th><th class="num">Symbols</th><th class="num">Edges</th><th class="num">Files</th></tr></thead>
    <tbody>${rows}</tbody></table>
  </div>`;
}

/* ---------- explore ---------- */
let _t;
function renderExplore(params){
  const q=$("#q");
  if(params.q!==undefined&&params.q!==q.value)q.value=params.q;
  q.oninput=()=>{clearTimeout(_t);_t=setTimeout(()=>{setParam("q",q.value.trim());doSearch();},180);};
  if(params.sym)brief(params.sym); else if(!$("#brief").dataset.sym)$("#brief").innerHTML=emptyState("Search a symbol","Type a name on the left, then pick a result to see its editing brief — body, callees, blast radius, tests, and compat.");
  doSearch();
}
async function doSearch(){
  const q=$("#q").value.trim(),hits=$("#hits");
  if(!q){hits.innerHTML="";return;}
  hits.innerHTML=skeleton(6);
  try{const rows=await api("search",{q,ref:S.ref,repo:S.repo});
    hits.innerHTML=(rows||[]).length? rows.map(hRow).join("") : emptyRow("no matches");
    $$("#hits .row").forEach(el=>el.onclick=()=>{go("explore",{q,sym:el.dataset.sym});});
  }catch(e){hits.innerHTML=emptyRow("error");toast(e.message);}
}
const hRow=h=>`<div class="row" data-sym="${esc(h.name)}"><span class="grow">${esc(h.name)}</span>${h.is_test?'<span class="pill">test</span>':""}</div>`;
async function brief(sym){
  const d=$("#brief");d.dataset.sym=sym;d.innerHTML=`<p class="muted"><span class="spinner"></span> loading brief…</p>`;
  $$("#hits .row").forEach(el=>el.classList.toggle("sel",el.dataset.sym===sym));
  try{const b=await api("brief",{symbol:sym,ref:S.ref,repo:S.repo});d.innerHTML=briefHTML(b);
    $$("#brief .row[data-sym]").forEach(el=>el.onclick=()=>go("explore",{q:$("#q").value.trim(),sym:el.dataset.sym}));
  }catch(e){d.innerHTML=emptyState("not found",esc(sym)+" is not indexed at "+esc(S.ref));}
}
function briefHTML(b){
  const t=b.target||{};
  const nb=(arr,title)=>arr&&arr.length?`<h2 class="sec">${title}</h2>`+arr.map(n=>
    `<div class="row" data-sym="${esc(n.handle||n.name)}"><span class="grow">${esc(n.name)}</span>${n.resolution_method?`<span class="pill">${esc(n.resolution_method)}</span>`:""}</div>`).join(""):"";
  let h=`<div class="detail-h"><h2>${esc(b.symbol)}</h2>${t.exported?'<span class="pill precise">exported</span>':""}</div>`;
  if(t.path)h+=`<div class="detail-path">${esc(t.path)}${t.start_line?":"+t.start_line:""}</div>`;
  if(t.body)h+=`<pre class="code">${esc(t.body)}</pre>`;
  h+=nb(b.callees,"Callees")+nb(b.callers,"Callers · blast radius");
  if(b.tests&&b.tests.length)h+=`<h2 class="sec">Covering tests</h2>`+b.tests.map(x=>`<div class="row"><span class="grow">${esc(x)}</span></div>`).join("");
  if(b.compat&&b.compat.length)h+=`<h2 class="sec">Compat across refs</h2>`+b.compat.map(c=>
    `<div class="row"><span class="tag ${esc(c.verdict)}">${esc(c.verdict)}</span><span class="grow">${esc(c.ref)}</span><span class="pill conf">conf ${c.confidence}</span></div>`).join("");
  if(b.intent)h+=`<h2 class="sec">Why</h2><pre class="code">${esc(JSON.stringify(b.intent,null,2))}</pre>`;
  return h;
}

/* ---------- diff ---------- */
function renderDiff(params){
  if(params.from)fillSel($("#from"),S.refs,params.from);
  if(params.to)fillSel($("#to"),S.refs,params.to);
  const run=()=>{setParam("from",$("#from").value);setParam("to",$("#to").value);doDiff();};
  $("#from").onchange=run;$("#to").onchange=run;$("#changed").onchange=doDiff;
  $("#diff-detail").innerHTML=emptyState("Pick two refs","Compare the indexed symbol set between refs — added, removed, shape- and behavior-changed.");
  doDiff();
}
async function doDiff(){
  const from=$("#from").value,to=$("#to").value,changes=$("#changes");
  if(!from||!to||from===to){changes.innerHTML=emptyRow("pick two different refs");return;}
  changes.innerHTML=skeleton(8);
  try{const d=await api("diff",{from,to,changed_only:$("#changed").checked,repo:S.repo});
    const c=d.changes||[];
    changes.innerHTML=`<div class="grouplabel">${esc(from)} → ${esc(to)} <span class="ln"></span><span class="pill">${c.length}</span></div>`+
      (c.length? c.map(x=>`<div class="row" data-sym="${esc(x.name)}"><span class="tag ${esc(x.change)}">${esc(x.change)}</span><span class="grow">${esc(x.name)}</span></div>`).join("") : emptyRow("no differences"));
    $$("#changes .row[data-sym]").forEach(el=>el.onclick=()=>diffBrief(el.dataset.sym));
  }catch(e){changes.innerHTML=emptyRow("error");toast(e.message);}
}
async function diffBrief(sym){
  const d=$("#diff-detail");d.innerHTML=`<p class="muted"><span class="spinner"></span> loading…</p>`;
  try{d.innerHTML=briefHTML(await api("brief",{symbol:sym,ref:$("#to").value,repo:S.repo}));}
  catch(e){d.innerHTML=emptyState("not found",esc(sym));}
}

/* ---------- impact ---------- */
function renderImpact(params){
  const x=$("#xq");
  if(params.symbol!==undefined&&params.symbol!==x.value)x.value=params.symbol;
  x.oninput=()=>{clearTimeout(_t);_t=setTimeout(()=>{setParam("symbol",x.value.trim());doImpact();},220);};
  if(!x.value)$("#impact-detail").innerHTML=emptyState("Enter a symbol","See who across every indexed repo calls it. Precise (module-resolved) callers are listed first.");
  else doImpact();
}
async function doImpact(){
  const s=$("#xq").value.trim(),d=$("#impact-detail");
  if(!s){d.innerHTML=emptyState("Enter a symbol","");return;}
  d.innerHTML=`<p class="muted"><span class="spinner"></span> scanning fleet…</p>`;
  try{const r=await api("ximpact",{symbol:s});
    const c=r.callers||[],precise=c.filter(x=>x.resolution_method==="import-resolved"),name=c.filter(x=>x.resolution_method!=="import-resolved");
    let h=`<div class="detail-h"><h2>${esc(s)}</h2>${r.contract_changed?'<span class="tag shape_changed">contract changed</span>':""}`+
      (r.stale_callers?`<span class="tag removed">${r.stale_callers} expect the old shape</span>`:"")+`</div>`;
    if(r.modules&&r.modules.length)h+=`<div class="detail-path">module ${esc(r.modules.join(", "))}</div>`;
    if(!c.length)h+=emptyRow("no external callers found");
    if(precise.length)h+=`<div class="grouplabel">module-resolved · precise <span class="ln"></span></div>`+precise.map(callerRow).join("");
    if(name.length)h+=`<div class="grouplabel">name-based · fallback <span class="ln"></span></div>`+name.map(callerRow).join("");
    if(r.note)h+=`<p class="muted" style="font-size:11.5px;margin-top:16px">${esc(r.note)}</p>`;
    d.innerHTML=h;
  }catch(e){d.innerHTML=emptyRow("error");toast(e.message);}
}
const callerRow=x=>`<div class="row"><span class="grow">${esc(x.repo)} <span class="muted">@${esc(x.ref)}</span> → ${esc(x.caller)}</span>`+
  (x.expected_signature==="stale"?'<span class="tag removed">old shape</span>':x.expected_signature==="current"?'<span class="tag compatible">current</span>':"")+
  (x.module?`<span class="pill mono">${esc(x.module)}</span>`:"")+
  `<span class="pill ${x.resolution_method==="import-resolved"?"precise":""}">${esc(x.resolution_method)}</span><span class="pill conf">${x.confidence}</span></div>`;

/* ---------- topics (ROS comms graph) ---------- */
function renderTopics(params){
  $("#tgraph").onchange=doTopics;$("#tconn").onchange=doTopics;
  const t=$("#tq");
  if(params.topic!==undefined&&params.topic!==t.value)t.value=params.topic;
  t.oninput=()=>{clearTimeout(_t);_t=setTimeout(()=>{setParam("topic",t.value.trim());doTopics();},220);};
  doTopics();
}
async function doTopics(){
  const topic=$("#tq").value.trim(),d=$("#topics-detail");
  d.innerHTML=`<p class="muted"><span class="spinner"></span> scanning fleet…</p>`;
  try{const r=await api("topics",topic?{topic,ref:FLEET_REF}:{ref:FLEET_REF});
    const g=r.groups||[];
    let h=`<div class="detail-h"><h2>${topic?esc(topic):"Communication graph"}</h2>`+
      `<span class="pill">${g.length} ${g.length===1?"group":"groups"}</span>`+
      (r.endpoints?`<span class="pill">${r.endpoints} endpoints</span>`:"")+
      (r.unresolved?`<span class="pill">${r.unresolved} dynamic</span>`:"")+`</div>`;
    if(!g.length){h+=emptyRow(r.note||"no endpoints found");d.innerHTML=h;return;}
    if($("#tgraph").checked){
      d.innerHTML=h+`<div class="canvas-host inline" id="tcanvas"></div>`;
      topicsGraph(g.filter(x=>!$("#tconn").checked||x.connected),!!topic);
      $("#tside").innerHTML=topic?g.map(topicGroup).join(""):`<p class="muted" style="font-size:12px;margin-top:10px">${g.length} names · click a topic for its endpoints</p>`;
      $$("#tside .row").forEach(()=>{});
      return;
    }
    h+=g.map(topicGroup).join("");
    if(r.note)h+=`<p class="muted" style="font-size:11.5px;margin-top:16px">${esc(r.note)}</p>`;
    d.innerHTML=h;$("#tside").innerHTML="";
  }catch(e){d.innerHTML=emptyRow("error");toast(e.message);}
}
function topicGroup(g){
  const conn=g.connected?`<span class="tag compatible">connected</span>`:`<span class="tag shape_changed">one-sided</span>`;
  const side=(eps,label)=>`<div class="grouplabel" style="margin-top:10px">${label} <span class="ln"></span><span class="pill">${(eps||[]).length}</span></div>`+
    ((eps||[]).length?eps.map(endpointRow).join(""):emptyRow("none"));
  return `<div class="card" style="margin-bottom:14px">
    <div class="card-head"><h3>${esc(g.name)}</h3><span class="badge">${esc(g.family)}</span>${conn}<span class="pill conf">conf ${g.confidence}</span></div>
    ${side(g.producers,g.family==="topic"?"publishers":g.family==="action"?"action servers":"service servers")}
    ${side(g.consumers,g.family==="topic"?"subscribers":g.family==="action"?"action clients":"service clients")}
  </div>`;
}
const endpointRow=e=>`<div class="row"><span class="grow">${esc(e.repo?e.repo+" · ":"")}${esc(e.path)}<span class="muted">:${e.line}</span>${e.in?` → ${esc(e.in)}`:""}</span>`+
  (e.msg_type?`<span class="pill mono">${esc(e.msg_type)}</span>`:"")+`<span class="pill">${esc(e.role)}</span></div>`;

/* ---------- usages (call sites, graph-verified) ---------- */
function renderUsages(params){
  const u=$("#uq");
  if(params.symbol!==undefined&&params.symbol!==u.value)u.value=params.symbol;
  u.oninput=()=>{clearTimeout(_t);_t=setTimeout(()=>{setParam("symbol",u.value.trim());doUsages();},220);};
  if(!u.value)$("#usages-detail").innerHTML=emptyState("Enter a symbol","Every call site across the fleet, with its exact line and enclosing function — confirmed against the call graph.");
  else doUsages();
}
async function doUsages(){
  const s=$("#uq").value.trim(),d=$("#usages-detail");
  if(!s){d.innerHTML=emptyState("Enter a symbol","");return;}
  d.innerHTML=`<p class="muted"><span class="spinner"></span> scanning fleet…</p>`;
  try{const r=await api("usages",{symbol:s,ref:FLEET_REF});
    const us=r.usages||[],conf=us.filter(x=>x.confirmed),lex=us.filter(x=>!x.confirmed);
    let h=`<div class="detail-h"><h2>${esc(r.symbol||s)}</h2><span class="pill">${r.total||0} call sites</span></div>`;
    if(!us.length)h+=emptyRow("no call sites found");
    if(conf.length)h+=`<div class="grouplabel">confirmed · in the call graph <span class="ln"></span><span class="pill">${conf.length}</span></div>`+conf.map(usageRow).join("");
    if(lex.length)h+=`<div class="grouplabel">lexical · comment/string or dynamic <span class="ln"></span><span class="pill">${lex.length}</span></div>`+lex.map(usageRow).join("");
    if(r.note)h+=`<p class="muted" style="font-size:11.5px;margin-top:16px">${esc(r.note)}</p>`;
    d.innerHTML=h;
  }catch(e){d.innerHTML=emptyRow("error");toast(e.message);}
}
const usageRow=x=>`<div class="row"><span class="grow"><span class="mono">${esc(x.repo?x.repo+" · ":"")}${esc(x.path)}<span class="muted">:${x.line}</span></span>${x.in?` → ${esc(x.in)}`:""}<div class="muted mono" style="font-size:11px;overflow:hidden;text-overflow:ellipsis">${esc(x.text)}</div></span>`+
  `<span class="pill ${x.confirmed?"precise":""}">${x.confirmed?"confirmed":"lexical"}</span></div>`;

/* ---------- verify (pre-commit safety net) ---------- */
function renderVerify(params){
  const p=$("#vpath");
  if(params.path!==undefined&&params.path!==p.value)p.value=params.path;
  p.onchange=()=>setParam("path",p.value.trim());
  $("#vrun").onclick=doVerify;
  if(!$("#verify-detail").dataset.ran)$("#verify-detail").innerHTML=emptyState("Paste a proposed edit","Give the file's repo-relative path and its full proposed content, then check what breaks — before saving or compiling.");
}
async function doVerify(){
  const path=$("#vpath").value.trim(),content=$("#vcontent").value,d=$("#verify-detail");
  if(!path){toast("Enter the repo-relative file path");return;}
  d.dataset.ran="1";
  d.innerHTML=`<p class="muted"><span class="spinner"></span> diffing signatures against the index…</p>`;
  try{
    const res=await fetch("/api/verify_edit?"+new URLSearchParams({path,repo:S.repo,ref:S.ref}),
      {method:"POST",headers:{"Content-Type":"application/x-www-form-urlencoded"},body:new URLSearchParams({content})});
    if(!res.ok)throw new Error("verify_edit: "+res.status+" "+(await res.text()).trim());
    const r=await res.json();
    let h=`<div class="detail-h"><h2>${esc(r.path)}</h2>${r.safe?'<span class="tag compatible">safe</span>':'<span class="tag shape_changed">breaks callers</span>'}</div>`;
    const chip=(names,tag,label)=>names&&names.length?`<div class="grouplabel">${label} <span class="ln"></span><span class="pill">${names.length}</span></div>`+
      names.map(n=>`<div class="row"><span class="tag ${tag}">${tag==="added"?"added":tag}</span><span class="grow">${esc(n)}</span></div>`).join(""):"";
    h+=chip(r.added,"added","new symbols")+chip(r.changed,"shape_changed","signature changed")+chip(r.removed,"removed","removed");
    for(const im of r.impacts||[]){
      h+=`<div class="card" style="margin-top:14px"><div class="card-head"><h3>${esc(im.symbol)}</h3><span class="tag ${im.kind==="removed"?"removed":"shape_changed"}">${esc(im.kind)}</span><span class="pill">${(im.breaks||[]).length} breaking call sites</span></div>`+
        (im.breaks||[]).map(usageRow).join("")+`</div>`;
    }
    if(!(r.impacts||[]).length&&r.safe)h+=emptyRow("no confirmed caller breaks");
    if(r.note)h+=`<p class="muted" style="font-size:11.5px;margin-top:16px">${esc(r.note)}</p>`;
    d.innerHTML=h;
  }catch(e){d.innerHTML=emptyRow("error");toast(e.message);}
}

/* ---------- graph helpers ---------- */
const pkgColor=p=>p==="(external)"?"var(--faint)":`hsl(${graphHue(p)} 62% 52%)`;
const confEdge=c=>c>=0.85?{width:1.8}:c>=0.7?{width:1.3}:{width:1.1,dashed:true};
let _gc=null,_fc=null,_lc=null,_tc=null;

/* ---------- call graph ---------- */
function renderGraph(params){
  const q=$("#gq");
  if(params.symbol!==undefined&&params.symbol!==q.value)q.value=params.symbol;
  if(params.depth)$("#gdepth").value=params.depth;
  q.oninput=()=>{clearTimeout(_t);_t=setTimeout(async()=>{const v=q.value.trim();if(!v){$("#ghits").innerHTML="";return;}
    try{const rows=await api("search",{q:v,ref:S.ref,repo:S.repo});
      $("#ghits").innerHTML=(rows||[]).slice(0,12).map(hRow).join("")||emptyRow("no matches");
      $$("#ghits .row").forEach(el=>el.onclick=()=>{$("#ghits").innerHTML="";go("graph",{symbol:el.dataset.sym,depth:$("#gdepth").value});});
    }catch(e){toast(e.message);}},200);};
  q.onkeydown=e=>{if(e.key==="Enter"){$("#ghits").innerHTML="";go("graph",{symbol:q.value.trim(),depth:$("#gdepth").value});}};
  $("#gdepth").onchange=()=>go("graph",{symbol:q.value.trim(),depth:$("#gdepth").value});
  $("#gtests").onchange=()=>doGraph(q.value.trim());$("#gext").onchange=()=>doGraph(q.value.trim());
  $("#gfit").onclick=()=>_gc&&_gc.fit();
  if(params.repo&&params.repo!==S.repo&&S.repos.includes(params.repo)){S.repo=params.repo;LS("repo",S.repo);fillSel($("#repo"),S.repos,S.repo);loadRefs().then(()=>doGraph(params.symbol));return;}
  if(params.symbol)doGraph(params.symbol);
  else $("#gcanvas").innerHTML=emptyState("Pick a symbol","Search on the left. Its callers are drawn to the left, its callees to the right, as many hops out as the depth.");
}
async function doGraph(sym){
  if(!sym)return;
  const host=$("#gcanvas");host.innerHTML=`<p class="muted" style="padding:20px"><span class="spinner"></span> loading neighborhood…</p>`;
  try{
    const r=await api("graph",{symbol:sym,repo:S.repo,ref:S.ref,depth:$("#gdepth").value,tests:$("#gtests").checked,external:$("#gext").checked,limit:140});
    if(!r.nodes.length){host.innerHTML=emptyState("not found",esc(r.note||sym));return;}
    const nodes=r.nodes.map(n=>({id:n.id,label:n.label,sub:n.pkg,col:n.hop,color:pkgColor(n.pkg),
      cls:(n.hop===0?"focus":"")+(n.pkg==="(external)"?" ext":"")+(n.test?" test":""),badge:[n.more?`+${n.more}`:"",n.external?`${n.external} lib`:""].filter(Boolean).join(" · "),
      title:n.id+(n.external?`\n${n.external} call(s) to library code not drawn`:"")}));
    const edges=r.edges.map(e=>Object.assign({from:e.from,to:e.to,title:`${e.method} · ${e.confidence}`},confEdge(e.confidence)));
    _gc=new GraphCanvas(host,{layered:true,colW:310,rowH:52,onSelect:n=>graphSide(n.id),onOpen:n=>go("graph",{symbol:n.id,depth:$("#gdepth").value})});
    _gc.render(nodes,edges,"layered");_gc.select(r.focus);
    graphSide(r.focus,r);
  }catch(e){host.innerHTML=emptyRow("error");toast(e.message);}
}
async function graphSide(sym,r){
  const d=$("#gside");
  let h=r&&(r.truncated||r.note)?`<p class="muted" style="font-size:11.5px">${r.truncated?"Neighborhood cut at 140 nodes (a +N badge marks hidden neighbors). ":""}${esc(r.note||"")}</p>`:"";
  d.innerHTML=h+`<p class="muted"><span class="spinner"></span> brief…</p>`;
  try{const b=await api("brief",{symbol:sym,ref:S.ref,repo:S.repo});d.innerHTML=h+briefHTML(b);
    $$("#gside .row[data-sym]").forEach(el=>el.onclick=()=>go("graph",{symbol:el.dataset.sym,depth:$("#gdepth").value}));
  }catch(e){d.innerHTML=h+`<div class="detail-path">${esc(sym)}</div>`;}
}

/* ---------- fleet map ---------- */
const LINK_COLORS={"http":"#2d6cdf","ros-topic":"#1a7f37","ros-service":"#bf8700","ros-action":"#cf222e","import":"#8250df","declared-dep":"#8b95a5"};
let _fm=null;
function renderFleet(){
  if(_fm){drawFleet();return;}
  $("#fcanvas").innerHTML=`<p class="muted" style="padding:20px"><span class="spinner"></span> mapping the fleet (the first time can take a while on a large fleet; it is cached after)…</p>`;
  api("fleetmap",{ref:FLEET_REF}).then(r=>{_fm=r;
    const kinds=[...new Set((r.links||[]).map(l=>l.kind))];
    $("#fkinds").innerHTML=kinds.map(k=>`<label><input type="checkbox" data-k="${esc(k)}" ${k==="declared-dep"?"":"checked"}> <span class="sw" style="background:${LINK_COLORS[k]||"#888"}"></span>${esc(k)}</label>`).join("")||`<span class="muted">no cross-repo links found</span>`;
    $("#fkinds").insertAdjacentHTML("beforeend",`<label><input type="checkbox" id="fall"> repos without links</label>`);
    $$("#fkinds input").forEach(i=>i.onchange=drawFleet);
    drawFleet();}).catch(e=>{$("#fcanvas").innerHTML=emptyRow("error");toast(e.message);});
}
function drawFleet(){
  const r=_fm,on=new Set($$("#fkinds input").filter(i=>i.checked).map(i=>i.dataset.k));
  const maxSym=Math.max(1,...(r.repos||[]).map(x=>x.symbols||0));
  const edgesOn=(r.links||[]).filter(l=>on.has(l.kind)),linked=new Set(edgesOn.flatMap(l=>[l.from,l.to]));
  const nodes=(r.repos||[]).filter(x=>$("#fall").checked||linked.has(x.repo)).map(x=>({id:x.repo,label:x.repo,shape:"circle",size:8+Math.sqrt((x.symbols||0)/maxSym)*26,
    color:`hsl(${graphHue(x.repo)} 55% 55%)`,title:`${x.repo}\n${x.summary||""}\n${num(x.files)} files · ${num(x.symbols)} symbols`}));
  const edges=edgesOn.map(l=>({from:l.from,to:l.to,color:LINK_COLORS[l.kind]||"#888",
    width:1+Math.log2(1+l.count),title:`${l.kind} ×${l.count}\n${(l.examples||[]).slice(0,3).join("\n")}`}));
  _fc=new GraphCanvas($("#fcanvas"),{minScale:0.35,spacing:120,onSelect:n=>fleetSide(n.id)});
  _fc.render(nodes,edges,"force");
}
function fleetSide(repo){
  const x=(_fm.repos||[]).find(r=>r.repo===repo)||{},ls=(_fm.links||[]).filter(l=>l.from===repo||l.to===repo);
  $("#fside").innerHTML=`<div class="card" style="margin-top:12px"><div class="card-head"><h3>${esc(repo)}</h3>${x.module?`<span class="badge">${esc(x.module)}</span>`:""}</div>
    <p class="muted" style="font-size:12.5px">${esc(x.summary||"")}</p>
    <div class="tiles" style="margin-top:10px"><div class="tile"><div class="n">${num(x.files)}</div><div class="l">files</div></div><div class="tile"><div class="n">${num(x.symbols)}</div><div class="l">symbols</div></div><div class="tile"><div class="n">${num(x.routes)}</div><div class="l">routes</div></div></div>
    <div class="muted mono" style="font-size:11.5px;margin-top:8px">${esc((x.languages||[]).join(" · "))}</div>
    ${ls.length?`<h2 class="sec">links</h2>`+ls.map(l=>`<div class="row"><span class="sw" style="background:${LINK_COLORS[l.kind]||"#888"}"></span><span class="grow">${esc(l.from)} → ${esc(l.to)}</span><span class="pill">${esc(l.kind)} ×${l.count}</span></div>`).join(""):""}
  </div>`;
}

/* ---------- links ---------- */
function renderLinks(params){
  const q=$("#lq");
  if(params.target!==undefined&&params.target!==q.value)q.value=params.target;
  q.onkeydown=e=>{if(e.key==="Enter")go("links",{target:q.value.trim()});};
  if(params.target)doLinks(params.target);
  else $("#lcanvas").innerHTML=emptyState("Enter a file or symbol","A playbook, a launch file, a Helm template, an ALICA plan, a class name — what names it as data, and what it names.");
}
async function doLinks(t){
  const host=$("#lcanvas");host.innerHTML=`<p class="muted" style="padding:20px"><span class="spinner"></span> loading…</p>`;
  try{const r=await api("links",{target:t,ref:FLEET_REF});
    const by=r.referred_by||[],to=r.refers_to||[];
    if(!by.length&&!to.length){host.innerHTML=emptyState("No references written as data",esc(t));$("#lside").innerHTML="";return;}
    const nodes=[{id:"@focus",label:t.split("/").pop(),sub:t,col:0,cls:"focus",color:"var(--accent)",title:t}],seen=new Set(["@focus"]),edges=[];
    const kindColor=k=>({path:"#2d6cdf",id:"#bf8700",template:"#8250df",values:"#8250df","names-symbol":"#1a7f37","rpc-impl":"#cf222e","openapi-route":"#cf222e"}[k]||"#8b95a5");
    for(const l of by){const id="L:"+l.repo+":"+l.path;if(!seen.has(id)){seen.add(id);nodes.push({id,label:l.path.split("/").pop(),sub:l.repo+":"+l.path.split("/").slice(0,-1).join("/"),col:-1,color:kindColor(l.kind),title:`${l.path}:${l.line}\n${l.text}`,path:l.path});}
      edges.push({from:id,to:"@focus",color:kindColor(l.kind),title:`[${l.kind}] ${l.path}:${l.line} ${l.text}`,width:1.5});}
    for(const l of to){const id="R:"+l.to;if(!seen.has(id)){seen.add(id);nodes.push({id,label:l.to.split("/").pop(),sub:l.to.split("/").slice(0,-1).join("/"),col:1,color:kindColor(l.kind),title:`${l.to}${l.to_line?":"+l.to_line:""}\nline ${l.line}: ${l.text}`,path:l.kind==="names-symbol"?"":l.to});}
      edges.push({from:"@focus",to:id,color:kindColor(l.kind),title:`[${l.kind}] line ${l.line}: ${l.text}`,width:1.5});}
    _lc=new GraphCanvas(host,{layered:true,colW:340,rowH:50,onOpen:n=>n.path&&go("links",{target:n.path})});
    _lc.render(nodes,edges,"layered");
    const row=(l,dir)=>`<div class="row" title="${esc(l.text)}"><span class="pill">${esc(l.kind)}</span><span class="grow">${dir==="in"?esc(l.path)+":"+l.line:esc(l.to)}</span></div>`;
    $("#lside").innerHTML=(by.length?`<h2 class="sec">referred to by</h2>`+by.map(l=>row(l,"in")).join(""):"")+(to.length?`<h2 class="sec">refers to</h2>`+to.map(l=>row(l,"out")).join(""):"");
  }catch(e){host.innerHTML=emptyRow("error");toast(e.message);}
}

/* ---------- topics as a graph ---------- */
function topicsGraph(groups,focused){
  const host=$("#tcanvas");
  const top=groups.slice().sort((a,b)=>((b.producers||[]).length+(b.consumers||[]).length)-((a.producers||[]).length+(a.consumers||[]).length)).slice(0,focused?groups.length:120);
  const nodes=[],edges=[],seen=new Set();
  const ep=(e,col)=>{const id=`E:${e.repo}:${e.path}`;if(!seen.has(id)){seen.add(id);
    nodes.push({id,label:(e.in||e.path.split("/").pop()),sub:e.repo+":"+e.path,col,shape:focused?undefined:"circle",size:6,
      color:`hsl(${graphHue(e.repo)} 55% 52%)`,title:`${e.repo}:${e.path}:${e.line}\n${e.text||""}`});}return id;};
  for(const g of top){const tid="T:"+g.family+":"+g.name;
    nodes.push({id:tid,label:"/"+g.name,col:focused?1:undefined,shape:focused?undefined:"circle",size:focused?undefined:5,cls:"topic"+(g.connected?"":" lonely"),
      color:g.connected?"var(--add)":"var(--beh)",title:`${g.family} /${g.name}\n${(g.producers||[]).length} producers · ${(g.consumers||[]).length} consumers`});
    for(const e of g.producers||[])edges.push({from:ep(e,0),to:tid,color:"var(--accent)",width:1.3,title:e.name_resolution||""});
    for(const e of g.consumers||[])edges.push({from:tid,to:ep(e,2),color:"var(--add)",width:1.3,title:e.name_resolution||""});
  }
  _tc=new GraphCanvas(host,{layered:focused,colW:360,rowH:46,minScale:focused?0.5:0.2,onSelect:n=>{if(n.id.startsWith("T:")){const name=n.id.split(":").slice(2).join(":");setParam("topic",name);$("#tq").value=name;doTopics();}}});
  _tc.render(nodes,edges,focused?"layered":"force");
}

/* ---------- coverage ---------- */
async function renderCoverage(){
  const b=$("#coverage-body");
  b.innerHTML=`<div class="h-eyebrow">Index</div><div class="page-h">Coverage</div><div class="page-sub">What the index stored of every repo, and every tracked file it skipped with the reason — so an empty answer can be told apart from files that were never read.</div><p class="muted"><span class="spinner"></span> reading…</p>`;
  try{const r=await api("coverage",{ref:FLEET_REF});
    b.querySelector("p.muted").remove();
    const reasons=[...new Set(r.repos.flatMap(x=>Object.keys(x.reasons||{})))];
    const col=k=>`hsl(${graphHue(k)} 45% 60%)`;
    b.insertAdjacentHTML("beforeend",`<div class="legend-row">${`<span><span class="sw" style="background:var(--accent)"></span>stored</span>`}${reasons.map(k=>`<span><span class="sw" style="background:${col(k)}"></span>${esc(k)}</span>`).join("")}</div>`);
    for(const x of r.repos.sort((a,b)=>(b.stored+b.skipped)-(a.stored+a.skipped))){
      const tot=Math.max(1,x.stored+x.skipped);
      const segs=`<span style="width:${x.stored/tot*100}%;background:var(--accent)" title="stored ${x.stored}"></span>`+
        Object.entries(x.reasons||{}).map(([k,n])=>`<span style="width:${n/tot*100}%;background:${col(k)}" title="${esc(k)}: ${n}"></span>`).join("");
      const kinds=(x.kinds||[]).slice(0,8).map(k=>`<tr><td class="name">${esc(k.kind)}</td><td class="mono">${num(k.stored)}</td><td class="mono">${num(Object.values(k.skipped||{}).reduce((a,b)=>a+b,0))}</td><td class="muted" style="font-size:11.5px">${esc(Object.keys(k.skipped||{}).join(", "))}</td></tr>`).join("");
      b.insertAdjacentHTML("beforeend",`<div class="card"><div class="card-head"><h3>${esc(x.repo)}</h3><span class="badge">${esc(x.ref)}</span>
        <span class="pill">${num(x.stored)} stored</span><span class="pill">${num(x.skipped)} skipped</span>${x.generated?`<span class="pill">${num(x.generated)} generated</span>`:""}${x.skips_recorded?"":'<span class="pill">re-index to record skips</span>'}</div>
        <div class="stackbar">${segs}</div>
        <table class="grid"><thead><tr><th>kind</th><th class="num">stored</th><th class="num">skipped</th><th>why</th></tr></thead><tbody>${kinds}</tbody></table></div>`);
    }
  }catch(e){b.insertAdjacentHTML("beforeend",emptyRow("error"));toast(e.message);}
}

/* ---------- shared bits ---------- */
function emptyState(title,hint){return `<div class="empty">
  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6"><rect x="3" y="4" width="18" height="16" rx="2"/><path d="M3 9h18M8 4v16" stroke-linecap="round"/></svg>
  <div class="t">${esc(title)}</div><div class="h">${hint}</div></div>`;}
const emptyRow=t=>`<div class="empty" style="padding:28px 10px"><div class="h">${esc(t)}</div></div>`;
const skeleton=n=>Array.from({length:n},()=>'<div class="skeleton"></div>').join("");

/* keyboard: "/" focuses the active view's search */
addEventListener("keydown",e=>{if(e.key==="/"&&!/input|select|textarea/i.test(e.target.tagName)){
  const {view}=parseHash();const t=view==="impact"?$("#xq"):view==="explore"?$("#q"):view==="topics"?$("#tq"):view==="usages"?$("#uq"):view==="verify"?$("#vpath"):view==="graph"?$("#gq"):view==="links"?$("#lq"):null;if(t){e.preventDefault();t.focus();}}});

boot();
