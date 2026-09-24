let state = {branch:null, branchVersion:0, detail:null, scenario:null, tab:'net'};
const $ = id => document.getElementById(id);
function banner(msg, kind){ const b=$('banner'); b.textContent=msg; b.className='banner show '+(kind||'info'); }
async function api(path, opts){
  const res = await fetch(path, opts);
  const txt = await res.text();
  let data; try{ data = txt?JSON.parse(txt):null; }catch(e){ data = txt; }
  if(!res.ok){ throw Object.assign(new Error((data&&data.error)||res.statusText), {status:res.status, data}); }
  return data;
}
async function loadCase(){
  try{ const r = await api('/api/case/load',{method:'POST'}); banner('案例已载入分支 '+r.BranchID+(r.Reused?'（幂等，身份不变）':''),'info'); await loadBranches(); state.branch=r.BranchID; await refreshBranch(); }
  catch(e){ banner('载入失败: '+e.message,'err'); }
}
async function buildCex(){
  try{ const r = await api('/api/case/counterexamples',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({branch_id:state.branch||'br_tokenflow-pack_v1',adopt_first:true})});
    banner('反例档案已生成 '+r.records.length+' 条（第一条采纳，其余未采纳均保留）','info'); await loadBranches(); }
  catch(e){ banner('反例生成失败: '+e.message,'err'); }
}
async function conflictDemo(){
  try{ const r = await api('/conflict/demo',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({branch_id:state.branch||'br_tokenflow-pack_v1'})});
    banner('窗口A '+JSON.stringify(r.window_a)+' ｜ 窗口B '+JSON.stringify(r.window_b.error? r.window_b.error.message: 'ok'), 'info');
    await refreshBranch(); }
  catch(e){ banner('冲突演示失败: '+e.message,'err'); }
}
async function loadBranches(){
  const r = await api('/api/branches');
  $('branches').innerHTML='';
  r.branches.forEach(b=>{
    const d=document.createElement('div');
    d.className='branch'+(state.branch===b.branch_id?' active':'');
    d.innerHTML=`<div><b>${b.name}</b></div><div class="mono">${b.branch_id}</div>
      <div class="small">src ${b.source_id.slice(0,26)}… v${b.version} events ${b.event_count}</div>`;
    d.onclick=()=>{ state.branch=b.branch_id; refreshBranch(); };
    $('branches').appendChild(d);
  });
}
async function refreshBranch(){
  if(!state.branch){ await loadBranches(); return; }
  await loadBranches();
  state.detail = await api('/api/branch?branch_id='+encodeURIComponent(state.branch));
  const d=state.detail;
  state.branchVersion = branchCurrentVersion();
  $('brtitle').textContent=d.doc.title+' @ '+d.doc.revision;
  $('ver').textContent='params '+d.params_version.slice(0,14)+'… / '+d.params.version;
  $('brver').textContent='v'+state.branchVersion;
  const sel=$('scenario'); sel.innerHTML='';
  d.scenarios.forEach(s=>{
    const o=document.createElement('option'); o.value=s.id;
    const ex=s.exact||{}; o.textContent=`${s.id} · ${ex.outcome||''}${ex.truncated?' (截断)':''}${(s.cover&&s.cover.unbounded)?' ω':''}`;
    sel.appendChild(o);
  });
  if(!state.scenario || !d.scenarios.find(s=>s.id===state.scenario)) state.scenario=d.scenarios[0].id;
  sel.value=state.scenario;
  renderCandidates(); renderInvs(); renderTimeline(); renderDiags(); renderAll();
}
async function renderAll(){
  const d=state.detail; if(!d) return;
  const sc=state.scenario;
  $('svg-net').src='/api/svg/net?branch_id='+enc(state.branch)+'&scenario_id='+enc(sc);
  $('svg-space').src='/api/svg/space?branch_id='+enc(state.branch)+'&scenario_id='+enc(sc);
  $('svg-cover').src='/api/svg/cover?branch_id='+enc(state.branch)+'&scenario_id='+enc(sc);
  const sum=d.scenarios.find(x=>x.id===sc);
  // marking path
  const ex=sum.exact||{};
  let pi=`策略 ${ex.strategy} · 预算 ${ex.budget} · 使用 ${ex.steps_used} · 结论 <b>${ex.outcome}</b> · 截断 ${ex.truncated}`;
  if(ex.goal_path_transitions&&ex.goal_path_transitions.length) pi+=`<br>目标 marking 路径: <span class="mono">S0 → ${ex.goal_path_transitions.join(' → ')} → ${ex.goal_state_id||''}</span>`;
  $('pathinfo').innerHTML=pi;
  // cover pairs
  const cv=sum.cover||{};
  let cp=(cv.cover_pairs||[]).map(p=>`<span class="tag t-omega">覆盖 ${p.covering_id} ⊱ ${p.covereded_id||p.covered_id} ω:${(p.omega_places||p.Places||[]).join(',')}</span>`).join('');
  cp += `<div class="small" style="margin-top:6px">unbounded=${cv.unbounded} · goal covered by: ${(cv.goal_covered_by||[]).join(', ')||'—'} · 截断=${cv.truncated}</div>`;
  $('coverpairs').innerHTML=cp;
  // deadlocks with reasons: pull run json to include enablement events
  let dh='';
  (ex.states||[]).filter(st=>st.deadlock).forEach(st=>{
    dh+=`<div class="cand"><b>${st.id}</b> <span class="tag t-dead">DEADLOCK</span><div class="mono">${st.canon} token</div>`;
    Object.entries(st.enablement||{}).forEach(([t,e])=>{
      if(!e.enabled) dh+=`<div class="small">${t}: 输入=${e.input_ok} 抑制=${e.inhibitor_ok} 容量=${e.capacity_ok} — ${(e.details||[]).join('; ')}</div>`;
    });
    dh+='</div>';
  });
  if(!dh) dh='<div class="small">无死锁候选。</div>';
  $('deadlock').innerHTML=dh;
  // frontier
  let fh='';
  if(ex.truncated || (ex.frontier_ids||[]).length){
    fh=`<div class="small">预算耗尽 → 结论 <b>unknown</b>，以下 frontier 状态保留了未展开的合法后继：</div>`;
    (ex.frontier_ids||[]).forEach(id=>{ const st=(ex.states||[]).find(s=>s.id===id);
      fh+=`<div class="cand"><span class="tag t-front">FRONTIER</span><b>${id}</b> <span class="mono">${st?st.canon:''}</span></div>`; });
  } else fh='<div class="small">本情形预算充足，未发生截断。</div>';
  $('frontier').innerHTML=fh;
  // raw
  const raw=await api('/api/scenario/raw?branch_id='+enc(state.branch)+'&scenario_id='+enc(sc));
  $('raw').textContent=JSON.stringify(raw,null,2);
  // machine readable events
  const evs=(ex.events||[]).concat(cv.events||[]);
  $('engineevents').textContent=JSON.stringify(evs,null,2);
}
function enc(s){return encodeURIComponent(s||'');}
function setTab(t){ state.tab=t; document.querySelectorAll('.tabs button').forEach(b=>b.classList.toggle('on',b.dataset.tab===t));
  ['net','space','cover','deadlock','frontier','raw'].forEach(k=>$('tab-'+k).style.display=(k===t?'block':'none')); }
function renderCandidates(){
  const d=state.detail;
  $('brver').textContent='v'+d.branch_version; state.branchVersion=d.branch_version;
  $('candidates').innerHTML='';
  d.candidates.filter(c=>c.Scenario===state.scenario||c.scenario_id===state.scenario).forEach(c=>{
    const div=document.createElement('div'); div.className='cand'+(c.stale?' stale':'');
    const tag = c.decision==='accepted'?'<span class="tag t-goal">已采纳</span>':c.decision==='rejected'?'<span class="tag t-dead">已否定</span>':c.decision==='adjusted'?'<span class="tag t-omega">参数调整</span>':'<span class="pill">待裁定</span>';
    div.innerHTML=`<div><b>${c.label}</b> ${tag} ${c.stale?'<span class="tag t-stale">失效范围</span>':''}</div>
      <div class="mono">${c.candidate_id}</div>
      <div class="small">类型 ${c.kind} · 结论 ${c.outcome} · run ${c.run_id}</div>
      ${c.stale_reason?`<div class="small">失效原因: ${c.stale_reason}</div>`:''}
      <div class="dep">来源: src=${(c.deps.source||[]).join(',')}<br>参数: ${(c.deps.params||[]).join(',')}<br>net_hash: ${(c.deps.net||[]).join(',')}<br>规则: ${(c.deps.rule||[]).join(',')}</div>`;
    const bar=document.createElement('div'); bar.className='row';
    [['accepted','采纳',btn=>btn.className='ok'],['rejected','否定',()=>{}],['adjusted','参数调整',()=>{}]].forEach(([dec,label])=>{
      const b=document.createElement('button'); b.textContent=label; b.style.padding='3px 8px';
      b.onclick=()=>decide(c.candidate_id,dec);
      bar.appendChild(b);
    });
    const u=document.createElement('button'); u.textContent='撤销'; u.className='danger'; u.style.padding='3px 8px';
    u.onclick=()=>undo(c.candidate_id); bar.appendChild(u);
    div.appendChild(bar);
    $('candidates').appendChild(div);
  });
}
async function decide(cid, decision){
  try{ const r=await api('/api/decide',{method:'POST',headers:{'Content-Type':'application/json'},
    body:JSON.stringify({branch_id:state.branch,candidate_id:cid,decision,reviewer:'reviewer-local',expected_version:state.branchVersion})});
    banner('已记录新事件，分支版本 v'+r.branch_version+'（原数据未被改写，可撤销）','info'); await refreshBranch(); }
  catch(e){ if(e.status===409){ banner('版本冲突：另一窗口已提交。当前='+JSON.stringify(e.data.conflict)+'；请刷新后重放差异，历史不被覆盖。','err'); }
    else banner('裁定失败: '+e.message,'err'); await refreshBranch(); }
}
async function undo(cid){
  await api('/api/decide/undo',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({branch_id:state.branch,candidate_id:cid,reviewer:'reviewer-local'})});
  banner('裁定已撤销，恢复前一分支状态（追加 reversal 事件，不重写历史）','info'); await refreshBranch();
}
async function undoFirst(){
  const d=state.detail; if(!d) return banner('请先选择分支','err');
  const decided=d.candidates.filter(c=>c.decision&&c.decision!=='open');
  if(!decided.length) return banner('当前分支没有可撤销的裁定','err');
  await undo(decided[decided.length-1].candidate_id);
}
function renderInvs(){
  const d=state.detail;
  if(!d.invalidations||!d.invalidations.length){ $('invs').innerHTML='<div>无失效记录（基线）。</div>'; return; }
  $('invs').innerHTML='<table><tr><th>seq</th><th>变化</th><th>受影响候选</th><th>原因</th></tr>'+
    d.invalidations.map(v=>`<tr><td>${v.seq}</td><td>${v.changed_type}</td><td class="mono">${v.candidate_id}</td><td>${v.reason}</td></tr>`).join('')+'</table>';
}
function renderDiags(){
  const d=state.detail; const sc=state.scenario;
  const sum=d.scenarios.find(x=>x.id===sc);
  const ds=sum.diagnostics||[];
  if(!ds.length){ $('diags').innerHTML='无诊断，所有领域量均带单位/坐标且可规范化。'; return; }
  $('diags').innerHTML=ds.map(x=>`<div class="cand"><b>${x.code}</b> @ ${x.field}<br>${x.message}<br><span class="mono">raw=${x.raw}</span></div>`).join('');
}
async function renderTimeline(){
  const tl=(await api('/api/branch/timeline?branch_id='+enc(state.branch))).timeline;
  $('timeline').innerHTML='';
  tl.forEach(ev=>{
    const d=document.createElement('div'); d.style.cssText='border-bottom:1px solid #edf2f7;padding:4px 2px;cursor:pointer';
    d.innerHTML=`<div>#${ev.seq} ${ev.type}</div><div class="small">src:${(ev.source_id||'').slice(0,18)} prm:${(ev.params_version||'').slice(0,12)}</div>`;
    d.title=JSON.stringify(ev.payload);
    d.onclick=()=>asOf(ev.seq);
    $('timeline').appendChild(d);
  });
}
async function asOf(seq){
  const r=await api('/api/branch/asof?branch_id='+enc(state.branch)+'&seq='+seq);
  banner('已回到事件 #'+seq+' 的可见结论（只读历史视图，未用最新结果覆盖）；当时裁定: '+JSON.stringify(r.decisions),'info');
}
async function reviseParams(){
  const d=state.detail;
  const p=Object.assign({}, d.params); p.version=p.version+'-budget90'; p.budget=90;
  try{ const r=await api('/api/revise/params',{method:'POST',headers:{'Content-Type':'application/json'},
    body:JSON.stringify({parent_branch:state.branch,params:p,reason:'预算60->90观察截断frontier变化'})});
    banner('新参数分支 '+r.branch_id+'；依赖计算已标出失效范围，旧结论保留','info'); state.branch=r.branch_id; await refreshBranch(); }
  catch(e){ banner('参数修订失败: '+e.message,'err'); }
}
async function reviseSourceV3(){
  const raw=await fetch('/api/case/load').then(r=>r.json());
  // transform baseline into v3 local change: concurrency arc ca1 weight 1->2
  const doc=raw.doc; doc.revision='v3-ui'; doc.title='局部源修订（UI）';
  const sc=doc.scenarios.find(x=>x.id==='concurrency');
  sc.arcs.find(a=>a.id==='ca1').raw_weight='2';
  try{ const r=await api('/api/revise/source',{method:'POST',headers:{'Content-Type':'application/json'},
    body:JSON.stringify({parent_branch:state.branch,doc:doc,reason:'仅concurrency ca1权重1->2'})});
    banner('局部源修订分支 '+r.branch_id+'；仅 concurrency 候选落入失效范围','info'); state.branch=r.branch_id; await refreshBranch(); }
  catch(e){ banner('源修订失败: '+e.message,'err'); }
}
async function exportArc(){
  const res=await fetch('/api/archive/export');
  const blob=await res.blob();
  const url=URL.createObjectURL(blob);
  const a=document.createElement('a'); a.href=url; a.download='tflocal-archive.json'; a.click();
  banner('归档已导出；删除本地数据库后可通过下方文件选择原样导入，身份/顺序/来源关系保持。','info');
}
$('arcfile').addEventListener('change', async ev=>{
  const f=ev.target.files[0]; if(!f) return;
  const text=await f.text();
  try{ const r=await api('/api/archive/import',{method:'POST',headers:{'Content-Type':'application/json'},body:text});
    banner('归档导入完成，max_event_seq='+r.max_event_seq+'（空库才能导入；身份与顺序恢复）','info'); await loadBranches(); }
  catch(e){ banner('导入失败(仅空库允许): '+e.message,'err'); }
});
loadBranches();
