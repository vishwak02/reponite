package interfaces

// dashboardHTML is the self-contained read-only dashboard served at "/". Vanilla
// JS + inline CSS (no external assets), fetching the /api/* JSON endpoints.
const dashboardHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>reponite</title>
<style>
  :root{--bg:#fff;--fg:#1a1a1a;--muted:#666;--line:#e2e2e2;--panel:#f7f7f8;--accent:#2d6cdf;
        --add:#1a7f37;--rem:#cf222e;--beh:#bf8700;--shape:#8250df}
  @media(prefers-color-scheme:dark){:root{--bg:#0d1117;--fg:#e6edf3;--muted:#8b949e;--line:#30363d;
        --panel:#161b22;--accent:#589bff;--add:#3fb950;--rem:#f85149;--beh:#d29922;--shape:#a371f7}}
  *{box-sizing:border-box}
  body{margin:0;font:14px/1.5 -apple-system,BlinkMacSystemFont,"Segoe UI",system-ui,sans-serif;color:var(--fg);background:var(--bg)}
  header{display:flex;gap:12px;align-items:center;padding:12px 18px;border-bottom:1px solid var(--line);flex-wrap:wrap}
  header h1{font-size:16px;margin:0;font-weight:700;letter-spacing:.2px}
  header .repo{color:var(--muted)}
  nav{display:flex;gap:4px;margin-left:auto}
  nav button{background:none;border:1px solid transparent;color:var(--muted);padding:5px 12px;border-radius:7px;cursor:pointer;font-size:13px}
  nav button.on{background:var(--panel);border-color:var(--line);color:var(--fg);font-weight:600}
  select,input{font:inherit;color:var(--fg);background:var(--bg);border:1px solid var(--line);border-radius:7px;padding:5px 8px}
  main{display:grid;grid-template-columns:minmax(260px,1fr) 2fr;gap:0;height:calc(100vh - 54px)}
  .side{border-right:1px solid var(--line);overflow:auto;padding:12px}
  .detail{overflow:auto;padding:16px 20px}
  .view{display:none}.view.on{display:block}
  .row{padding:6px 9px;border-radius:7px;cursor:pointer;font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-size:12.5px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
  .row:hover{background:var(--panel)}
  .tag{display:inline-block;font-size:10.5px;font-weight:700;padding:1px 6px;border-radius:10px;margin-right:6px;vertical-align:middle}
  .added{color:var(--add);border:1px solid var(--add)} .removed{color:var(--rem);border:1px solid var(--rem)}
  .behavior_changed{color:var(--beh);border:1px solid var(--beh)} .shape_changed{color:var(--shape);border:1px solid var(--shape)}
  .compatible,.unchanged{color:var(--muted);border:1px solid var(--line)} .absent{color:var(--muted);border:1px dashed var(--line)}
  h2{font-size:13px;text-transform:uppercase;letter-spacing:.5px;color:var(--muted);margin:18px 0 8px}
  pre{background:var(--panel);border:1px solid var(--line);border-radius:9px;padding:12px;overflow:auto;font-size:12.5px;margin:0}
  .k{color:var(--muted)} .mono{font-family:ui-monospace,SFMono-Regular,Menlo,monospace}
  .pill{font-size:11px;color:var(--muted);border:1px solid var(--line);border-radius:10px;padding:1px 7px;margin-left:6px}
  .empty{color:var(--muted);padding:20px 4px}
  a{color:var(--accent);text-decoration:none;cursor:pointer}
</style>
</head>
<body>
<header>
  <h1>reponite</h1><span class="repo" id="repo"></span>
  <label class="k">ref <select id="ref"></select></label>
  <nav>
    <button data-v="explore" class="on">Explore</button>
    <button data-v="diff">Diff</button>
    <button data-v="impact">Impact</button>
  </nav>
</header>
<main>
  <div class="side">
    <div id="explore-side">
      <input id="q" placeholder="search symbols…" style="width:100%">
      <div id="hits" style="margin-top:10px"></div>
    </div>
    <div id="diff-side" style="display:none">
      <label class="k">from <select id="from"></select></label><br><br>
      <label class="k">to <select id="to"></select></label><br><br>
      <label class="k"><input type="checkbox" id="changed" checked> changed only</label>
      <div id="changes" style="margin-top:12px"></div>
    </div>
    <div id="impact-side" style="display:none">
      <input id="xq" placeholder="external symbol name…" style="width:100%">
      <p class="k" style="font-size:12px">Who across indexed repos calls this symbol.</p>
      <div id="xcallers"></div>
    </div>
  </div>
  <div class="detail">
    <div id="explore" class="view on"><div class="empty">Search and pick a symbol to see its brief.</div></div>
    <div id="diff" class="view"><div class="empty">Pick two refs to diff.</div></div>
    <div id="impact" class="view"><div class="empty">Enter a symbol to see cross-repo callers.</div></div>
  </div>
</main>
<script>
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

// Impact
async function impact(){
  const s=$("#xq").value.trim(); if(!s){$("#impact").innerHTML='<div class="empty">Enter a symbol.</div>';return;}
  const d=await api("ximpact",{symbol:s});
  const c=d.callers||[];
  $("#impact").innerHTML='<h2>Callers of '+esc(s)+'</h2>'+(c.length?c.map(x=>'<div class="row">'+esc(x.repo)+' <span class="k">@'+esc(x.ref)+'</span> → '+esc(x.caller)+' <span class="pill">conf '+x.confidence+'</span></div>').join(''):'<div class="empty">no external callers found</div>')+'<p class="k" style="font-size:11.5px;margin-top:14px">'+esc(d.note||"")+'</p>';
}
$("#xq").oninput=()=>{clearTimeout(window._x);window._x=setTimeout(impact,220);};

boot();
</script>
</body>
</html>`
