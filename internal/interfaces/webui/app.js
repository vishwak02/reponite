"use strict";
// reponite dashboard — vanilla JS SPA. Hash-routed + deep-linkable, state
// persisted to localStorage, with loading / empty / error states throughout.
const $=s=>document.querySelector(s), $$=s=>[...document.querySelectorAll(s)];
const esc=s=>String(s==null?"":s).replace(/[&<>"]/g,c=>({"&":"&amp;","<":"&lt;",">":"&gt;",'"':"&quot;"}[c]));
const api=(p,q)=>fetch("/api/"+p+"?"+new URLSearchParams(q)).then(r=>{if(!r.ok)throw new Error(p+": "+r.status);return r.json();});
const num=n=>(n==null?"–":n.toLocaleString());
const LS=(k,v)=>v===undefined?localStorage.getItem("reponite."+k):localStorage.setItem("reponite."+k,v);

const S={repo:"",ref:"HEAD",repos:[],refs:[],overview:null};

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

const VIEWS={overview:renderOverview,explore:renderExplore,diff:renderDiff,impact:renderImpact,topics:renderTopics};
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
    let h=`<div class="detail-h"><h2>${esc(s)}</h2>${r.contract_changed?'<span class="tag shape_changed">contract changed</span>':""}</div>`;
    if(r.modules&&r.modules.length)h+=`<div class="detail-path">module ${esc(r.modules.join(", "))}</div>`;
    if(!c.length)h+=emptyRow("no external callers found");
    if(precise.length)h+=`<div class="grouplabel">module-resolved · precise <span class="ln"></span></div>`+precise.map(callerRow).join("");
    if(name.length)h+=`<div class="grouplabel">name-based · fallback <span class="ln"></span></div>`+name.map(callerRow).join("");
    if(r.note)h+=`<p class="muted" style="font-size:11.5px;margin-top:16px">${esc(r.note)}</p>`;
    d.innerHTML=h;
  }catch(e){d.innerHTML=emptyRow("error");toast(e.message);}
}
const callerRow=x=>`<div class="row"><span class="grow">${esc(x.repo)} <span class="muted">@${esc(x.ref)}</span> → ${esc(x.caller)}</span>`+
  (x.module?`<span class="pill mono">${esc(x.module)}</span>`:"")+
  `<span class="pill ${x.resolution_method==="import-resolved"?"precise":""}">${esc(x.resolution_method)}</span><span class="pill conf">${x.confidence}</span></div>`;

/* ---------- topics (ROS comms graph) ---------- */
function renderTopics(params){
  const t=$("#tq");
  if(params.topic!==undefined&&params.topic!==t.value)t.value=params.topic;
  t.oninput=()=>{clearTimeout(_t);_t=setTimeout(()=>{setParam("topic",t.value.trim());doTopics();},220);};
  doTopics();
}
async function doTopics(){
  const topic=$("#tq").value.trim(),d=$("#topics-detail");
  d.innerHTML=`<p class="muted"><span class="spinner"></span> scanning fleet…</p>`;
  try{const r=await api("topics",topic?{topic,repo:S.repo}:{repo:S.repo});
    const g=r.groups||[];
    let h=`<div class="detail-h"><h2>${topic?esc(topic):"Communication graph"}</h2>`+
      `<span class="pill">${g.length} ${g.length===1?"group":"groups"}</span>`+
      (r.endpoints?`<span class="pill">${r.endpoints} endpoints</span>`:"")+
      (r.unresolved?`<span class="pill">${r.unresolved} dynamic</span>`:"")+`</div>`;
    if(!g.length){h+=emptyRow(r.note||"no endpoints found");}
    else h+=g.map(topicGroup).join("");
    if(g.length&&r.note)h+=`<p class="muted" style="font-size:11.5px;margin-top:16px">${esc(r.note)}</p>`;
    d.innerHTML=h;
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

/* ---------- shared bits ---------- */
function emptyState(title,hint){return `<div class="empty">
  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6"><rect x="3" y="4" width="18" height="16" rx="2"/><path d="M3 9h18M8 4v16" stroke-linecap="round"/></svg>
  <div class="t">${esc(title)}</div><div class="h">${hint}</div></div>`;}
const emptyRow=t=>`<div class="empty" style="padding:28px 10px"><div class="h">${esc(t)}</div></div>`;
const skeleton=n=>Array.from({length:n},()=>'<div class="skeleton"></div>').join("");

/* keyboard: "/" focuses the active view's search */
addEventListener("keydown",e=>{if(e.key==="/"&&!/input|select|textarea/i.test(e.target.tagName)){
  const {view}=parseHash();const t=view==="impact"?$("#xq"):view==="explore"?$("#q"):view==="topics"?$("#tq"):null;if(t){e.preventDefault();t.focus();}}});

boot();
