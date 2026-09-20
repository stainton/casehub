const $=s=>document.querySelector(s), $$=s=>[...document.querySelectorAll(s)];
let state=null, focus=null, selected=new Map(), view='cases', modalSave=null, recordCase=null, recordTask='', recordEditor=null, recordViewers=[], recordHistoryLimit=3;
let openTasks=new Set(), closedTaskVersions=new Set(), closedFolders=new Set(), closedReqFolders=new Set(), closedReviewFolders=new Set(), closedVersions=new Set();
let page='cases', reqFocus=null, reqEditor=null, reqSidebarWidth=null, aiDoc=null, reqPage='docs';
let aiSource=null, aiPlannerEnabled=null;
let caseViewMode='friendly'; // 'friendly' | 'raw' — applies to whichever case detail is currently shown
let caseViewFor=null; // simplifyKey of the case caseViewMode was chosen for; opening another case re-picks the default
const esc=s=>String(s??'').replace(/[&<>'"]/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;',"'":'&#39;','"':'&quot;'}[c]));
const fmt=s=>s?new Date(s).toLocaleString():'—';
const version=id=>state.versions.find(v=>v.id===id), folders=id=>state.folders.filter(f=>f.VersionID===id), cases=id=>state.cases.filter(c=>c.VersionID===id);
async function request(path,options){let r=await fetch(path,options),x=await r.json();if(!r.ok){let e=Error(x.error||'请求失败');e.status=r.status;e.conflicts=x.conflicts;throw e}return x}
function normalizeState(s){s=s||{};for(const key of ['versions','folders','cases','histories','records','tasks','reqFolders','reqDocs','pendingFolders','pendingCases','scripts'])if(!Array.isArray(s[key]))s[key]=[];return s}
async function refresh(){state=normalizeState(await request('/api/state'));render();}
async function act(type,data={},retry=false){try{let out=await request('/api/action',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({Type:type,Author:'本地用户',...data})});state=normalizeState(out.state);if(out.warnings?.length)toast(out.warnings.join('；'));render();return out}catch(e){if(e.status===409&&type.startsWith('merge')){alert(`${e.message}\n冲突用例：${(e.conflicts||[]).join(', ')}\n请拉取主线，然后打开冲突用例编辑并确认人工处理。`)}else if(e.status===409&&!retry&&confirm(`${e.message}\n冲突用例：${(e.conflicts||[]).join(', ')}\n是否以当前编辑内容作为人工解决结果？`))return act(type,{...data,Force:true},true);toast(e.message,true);throw e}}
function render(){ renderVersions();renderTasks();renderFocus();if(recordTask)$('#edit-case')?.remove();updateBulk();if(location.hash)renderHistoryRoute();renderReqTree();renderReviewTree();renderScriptTree();renderAutoFocus(); }
function renderVersions(){let box=$('#versions');box.innerHTML=state.versions.map(v=>`<div class="version" data-version="${v.id}"><div class="version-title"><span class="chev">⌄</span><span>${esc(v.name)}</span><span class="badge">${v.mainline?'只读主线':'测试版本'}</span>${v.mainline?'':`<span class="version-actions"><button data-sync="${v.id}">拉取主线</button><button data-merge="${v.id}">合并</button><button data-delete-version="${v.id}" class="danger">删除</button></span>`}</div><div class="version-body">${tree(v.id)}</div></div>`).join('');bindTree(box);}
function tree(vid,onlyIDs=null,taskID=''){let fs=folders(vid),cs=cases(vid),roots=fs.filter(f=>!f.ParentID||!fs.some(x=>x.ID===f.ParentID));let branch=!version(vid).mainline,showCheck=branch&&!taskID;function node(f,depth){let children=fs.filter(x=>x.ParentID===f.ID),own=cs.filter(c=>c.FolderID===f.ID);let visible=!onlyIDs||own.some(c=>onlyIDs.has(c.ID))||children.some(ch=>hasHit(ch));if(!visible)return'';return `<div class="tree-row folder-row" style="padding-left:${8+depth*17}px" data-folder="${f.ID}" data-version="${vid}">${showCheck?'<input class="folder-check" type="checkbox">':''}<span class="chev">▾</span><span>📁</span><span class="label">${esc(f.Name)}</span></div><div>${own.filter(c=>!onlyIDs||onlyIDs.has(c.ID)).map(c=>caseRow(c,depth+1,branch&&!taskID,taskID)).join('')}${children.map(ch=>node(ch,depth+1)).join('')}</div>`}function hasHit(f){return cs.some(c=>c.FolderID===f.ID&&onlyIDs.has(c.ID))||fs.filter(x=>x.ParentID===f.ID).some(hasHit)}return roots.map(r=>node(r,0)).join('')||'<p class="meta">空版本</p>'}
// 用例树（版本树、测试任务、用例评审）显示"<用例编号> <用例名称>"，完整内容也放在悬停提示里（名称过长会被截断）。
const caseLabel=c=>`${c.ID} ${c.Title}`;
function caseRow(c,depth,branch,taskID=''){let checked=selected.get(c.VersionID)?.has(c.ID);return `<div class="tree-row ${taskID?'task-case':'case-row'}" style="padding-left:${8+depth*17}px" data-case="${c.ID}" data-c="${c.ID}" data-version="${c.VersionID}" data-v="${c.VersionID}" ${taskID?`data-task="${taskID}"`:''} title="${esc(caseLabel(c))}">${branch?`<input class="case-check" type="checkbox" ${checked?'checked':''}>`:''}<span class="label">${esc(caseLabel(c))}</span>${c.Result?`<span class="result ${c.Result}"></span>`:''}</div>`}
function bindTree(root){root.querySelectorAll('.version-title').forEach(e=>{let v=e.parentElement,key=v.dataset.version;if(closedVersions.has(key))v.classList.add('closed');e.onclick=x=>{if(x.target.closest('button'))return;let closed=v.classList.toggle('closed');closed?closedVersions.add(key):closedVersions.delete(key)}});root.querySelectorAll('.folder-row').forEach(e=>{let key=`${e.dataset.version}:${e.dataset.folder}`;if(closedFolders.has(key))e.classList.add('closed');e.querySelector('.chev').onclick=x=>{x.stopPropagation();let closed=e.classList.toggle('closed');closed?closedFolders.add(key):closedFolders.delete(key)};let fc=e.querySelector('.folder-check');if(fc)fc.onclick=x=>{x.stopPropagation();toggleFolderSelect(e.dataset.version,e.dataset.folder,fc.checked)};e.onclick=x=>{if(x.target.matches('input'))return;setFocus({type:'folder',versionID:e.dataset.version,id:e.dataset.folder})};e.oncontextmenu=x=>menu(x,folderMenu(e.dataset.version,e.dataset.folder))});root.querySelectorAll('.case-row').forEach(e=>{e.onclick=x=>{if(x.target.matches('input')){toggleSelect(e.dataset.version,e.dataset.case,x.target.checked);return}setFocus({type:'case',versionID:e.dataset.version,id:e.dataset.case})};e.oncontextmenu=x=>menu(x,caseMenu(e.dataset.version,e.dataset.case))});root.querySelectorAll('[data-sync]').forEach(e=>e.onclick=()=>act('sync',{VersionID:e.dataset.sync}));root.querySelectorAll('[data-merge]').forEach(e=>e.onclick=()=>{if(confirm('将此版本中的文本变更与新增目录合并到只读主线？'))act('merge',{VersionID:e.dataset.merge})});root.querySelectorAll('[data-delete-version]').forEach(e=>e.onclick=()=>{if(confirm('确定删除这个测试版本？其目录、用例、测试任务和执行记录都会一并删除，且无法恢复。'))act('deleteVersion',{VersionID:e.dataset.deleteVersion})});updateFolderChecks(root)}
function updateFolderChecks(root=document){root.querySelectorAll('.folder-row').forEach(e=>{let fc=e.querySelector('.folder-check');if(!fc)return;let f=state.folders.find(x=>x.VersionID===e.dataset.version&&x.ID===e.dataset.folder);if(!f)return;let subIDs=[f.ID,...descendantFolders(f)],all=cases(e.dataset.version).filter(c=>subIDs.includes(c.FolderID)),selSet=selected.get(e.dataset.version),selCount=all.filter(c=>selSet?.has(c.ID)).length;fc.checked=all.length>0&&selCount===all.length;fc.indeterminate=selCount>0&&selCount<all.length})}
function toggleFolderSelect(vid,folderId,checked){let f=state.folders.find(x=>x.VersionID===vid&&x.ID===folderId);if(!f)return;let subIDs=[f.ID,...descendantFolders(f)];if(!selected.has(vid))selected.set(vid,new Set());let s=selected.get(vid);cases(vid).filter(c=>subIDs.includes(c.FolderID)).forEach(c=>checked?s.add(c.ID):s.delete(c.ID));updateBulk();renderVersions()}
function setFocus(x){if(!$('#drawer').classList.contains('hidden')&&!(x.type==='case'&&recordCase?.ID===x.id))closeRecordDrawer();if(location.hash)history.replaceState(null,'',location.pathname+location.search);focus=x;recordTask=x.taskID||'';renderVersions();renderFocus()}
function closeRecordDrawer(){$('#record-form').reset();recordEditor?.setMarkdown('');$('#drawer').classList.add('hidden')}
// ---- 用例详情：Planner 原始内容 / 阅读友好版 切换 ----------------------------
// Planner 生成的用例是给 generator 用的，步骤/预期结果信息密度很高，人读起来负担大。
// 阅读友好版由 auto-test 的 /v1/planner/simplify 改写生成，和用例一样持久化在
// State 里（不是浏览器缓存）；SimplifiedFrom* 是生成时源文本的快照，用来判断
// 用例后续被编辑后阅读友好版是否已经过时。
function caseHasSimplified(c){return !!(c&&c.SimplifiedSteps)}
function caseSimplifiedStale(c){return c.SimplifiedFromPreconditions!==(c.Preconditions||'')||c.SimplifiedFromSteps!==(c.Steps||'')||c.SimplifiedFromExpected!==(c.Expected||'')}
function caseViewToggleHTML(){return `<div class="segmented case-view-toggle"><button type="button" data-case-view="friendly" class="${caseViewMode==='friendly'?'active':''}">阅读友好版</button><button type="button" data-case-view="raw" class="${caseViewMode==='raw'?'active':''}">Planner 原始内容</button></div>`}
function caseFieldLinesHTML(preconditions,steps,expected){return `<div class="case-lines"><div class="field-line"><h4>前置条件</h4><p>${esc(preconditions||'—')}</p></div><div class="field-line"><h4>执行步骤</h4><p>${esc(steps||'—')}</p></div><div class="field-line"><h4>预期结果</h4><p>${esc(expected||'—')}</p></div></div>`}
// 用例描述不由 AI 填写：评审发起人读完用例后人工总结，其他评审人先看描述再看细节，负担更小。
// 与阅读友好版/原始内容切换无关，两种视图下都显示在最前面。
function caseDescriptionHTML(c,isPending){
  const edit=caseEditFor(c,isPending);
  if(edit?.mode==='describe')return `<div class="case-description case-edit-form"><p class="meta">阅读用例后写一段总结（测试目标、覆盖范围、需要评审人特别注意的地方），保存不会重置评审状态。</p>${caseEditArea('Description','用例描述',edit.draft.Description)}${caseEditActionsHTML()}</div>`;
  const empty=isPending?'尚未填写 —— 由评审发起人阅读用例后总结，方便其他评审人快速了解这条用例':'未填写';
  return `<div class="case-description"><div class="field-line"><h4>用例描述</h4>${c.Description?`<p>${esc(c.Description)}</p>`:`<p class="meta">${empty}</p>`}</div>${isPending?`<button type="button" class="secondary" id="case-describe-btn">${c.Description?'编辑描述':'填写描述'}</button>`:''}</div>`;
}
// ---- 页面内编辑（不弹窗）------------------------------------------------------
// mode: 'case' 整条用例 / 'describe' 用例描述（仅评审区）/ 'friendly' 阅读友好版。
// 用例管理和用例评审两个详情面板各自最多一条用例处于编辑态；草稿存在这里，其他操作触发重渲染时输入不丢，
// 在该面板打开另一条用例时丢弃。
const caseEdits={cases:null,review:null};
const casePane=isPending=>isPending?'review':'cases';
function caseEditFor(c,isPending){const e=caseEdits[casePane(isPending)];return e&&e.key===simplifyKey(c,isPending)?e:null}
function startCaseEdit(c,isPending,mode){
  const draft=mode==='case'?{Title:c.Title||'',Priority:c.Priority||'P0',Description:c.Description||'',Preconditions:c.Preconditions||'',Steps:c.Steps||'',Expected:c.Expected||''}
    :mode==='describe'?{Description:c.Description||''}
    :{SimplifiedPreconditions:c.SimplifiedPreconditions||'',SimplifiedSteps:c.SimplifiedSteps||'',SimplifiedExpected:c.SimplifiedExpected||''};
  caseEdits[casePane(isPending)]={key:simplifyKey(c,isPending),mode,draft};
  if(mode==='friendly'){caseViewFor=simplifyKey(c,isPending);caseViewMode='friendly'}
}
function caseEditArea(field,label,value,required=false){
  const rows=Math.min(14,Math.max(3,String(value||'').split('\n').length+1));
  return `<label>${label}<textarea data-edit-field="${field}" rows="${rows}"${required?' required':''}>${esc(value)}</textarea></label>`;
}
function caseEditActionsHTML(){return `<p class="drawer-actions case-edit-actions"><button type="button" class="secondary" data-edit-cancel>取消</button><button type="button" data-edit-save>保存</button></p>`}
function caseFullEditHTML(c,isPending,d){
  return `<div class="case-edit-form">${isPending?'<p class="meta">保存后评审状态会重置为待评审。</p>':''}<label>标题<input data-edit-field="Title" required value="${esc(d.Title)}"></label><label>优先级<select data-edit-field="Priority">${['P0','P1','P2','P3'].map(x=>`<option ${d.Priority===x?'selected':''}>${x}</option>`).join('')}</select></label>${caseEditArea('Description','用例描述（评审时人工填写，可选）',d.Description)}${caseEditArea('Preconditions','前置条件',d.Preconditions)}${caseEditArea('Steps','执行步骤',d.Steps)}${caseEditArea('Expected','预期结果',d.Expected)}${caseEditActionsHTML()}</div>`;
}
async function saveCaseEdit(c,isPending,rerender,btn){
  const pane=casePane(isPending),edit=caseEdits[pane],d=edit.draft;
  let type,payload,done;
  if(edit.mode==='case'){
    if(!d.Title.trim())return toast('用例标题不能为空',true);
    type=isPending?'editPendingCase':'editCase';payload={...d,CaseID:c.ID,FolderID:c.FolderID};done='用例已保存';
  }else if(edit.mode==='describe'){
    type='describePendingCase';payload={CaseID:c.ID,Description:d.Description};done='用例描述已保存';
  }else{
    if(!d.SimplifiedSteps.trim())return toast('阅读友好版步骤不能为空',true);
    type=isPending?'simplifyPendingCase':'simplifyCase';payload={CaseID:c.ID,...d};done='阅读友好版本已保存';
  }
  if(!isPending&&type!=='describePendingCase')payload.VersionID=c.VersionID;
  btn.disabled=true;
  try{await (isPending?actReview:act)(type,payload)}catch{btn.disabled=false;return} // act 已提示失败原因
  if(caseEdits[pane]===edit)caseEdits[pane]=null;
  toast(done);rerender();
}
function caseDetailBodyHTML(c,isPending){
  const has=caseHasSimplified(c),stale=has&&caseSimplifiedStale(c),fresh=has&&!stale;
  // 每打开一条用例重新选默认视图：有（未过时的）阅读友好版优先展示，否则展示 Planner 原始内容。
  // 同一条用例内用户手动切换的视图在重渲染时保持不变。
  const key=simplifyKey(c,isPending),pane=casePane(isPending);
  if(caseEdits[pane]&&caseEdits[pane].key!==key)caseEdits[pane]=null; // 打开了另一条用例：丢弃未保存的编辑
  if(caseViewFor!==key){caseViewFor=key;caseViewMode=fresh?'friendly':'raw'}
  const edit=caseEdits[pane];
  if(edit?.mode==='case')return caseFullEditHTML(c,isPending,edit.draft);
  const toggle=caseDescriptionHTML(c,isPending)+caseViewToggleHTML();
  if(edit?.mode==='friendly')return `${toggle}<div class="case-edit-form case-lines">${caseEditArea('SimplifiedPreconditions','前置条件',edit.draft.SimplifiedPreconditions)}${caseEditArea('SimplifiedSteps','执行步骤',edit.draft.SimplifiedSteps,true)}${caseEditArea('SimplifiedExpected','预期结果',edit.draft.SimplifiedExpected)}${caseEditActionsHTML()}</div>`;
  const prompt=fresh?'':`<div class="case-simplify-prompt"><p class="meta">${stale?'用例内容已更改，之前生成的阅读友好版本已过时。':'还没有阅读友好版本 —— 这份用例的步骤/预期结果是给 Planner/生成器看的原始内容，信息密度较高，供人阅读负担较大。'}</p><div class="case-simplify-actions"><button type="button" id="case-simplify-btn">${has?'重新生成阅读友好版本':'生成阅读友好版本'}</button>${has?'<button type="button" class="secondary" id="case-simplify-edit-btn">编辑旧版本</button>':''}</div></div>`;
  // 原始内容页只展示原始内容；生成/过时提示只在"阅读友好版"页出现。
  if(caseViewMode!=='friendly')return `${toggle}${caseFieldLinesHTML(c.Preconditions,c.Steps,c.Expected)}`;
  if(!fresh)return `${toggle}${prompt}`;
  return `${toggle}${caseFieldLinesHTML(c.SimplifiedPreconditions,c.SimplifiedSteps,c.SimplifiedExpected)}<p class="meta case-simplify-meta">阅读友好版 · 更新于 ${fmt(c.SimplifiedAt)} <button type="button" class="secondary" id="case-simplify-edit-btn">编辑</button><button type="button" class="secondary" id="case-simplify-btn">重新生成</button></p>`;
}
function bindCaseDetailBody(root,c,isPending,rerender){
  const edit=caseEditFor(c,isPending);
  root.querySelectorAll('[data-case-view]').forEach(b=>b.onclick=()=>{
    if(edit?.mode==='friendly'&&b.dataset.caseView!=='friendly')caseEdits[casePane(isPending)]=null;
    caseViewMode=b.dataset.caseView;rerender()});
  const headerEdit=root.querySelector('#edit-case,#edit-review-case');
  if(headerEdit){headerEdit.disabled=edit?.mode==='case';headerEdit.onclick=()=>{startCaseEdit(c,isPending,'case');rerender()}}
  root.querySelectorAll('[data-edit-field]').forEach(el=>el.oninput=el.onchange=()=>{if(edit)edit.draft[el.dataset.editField]=el.value});
  root.querySelector('[data-edit-cancel]')?.addEventListener('click',()=>{caseEdits[casePane(isPending)]=null;rerender()});
  const saveBtn=root.querySelector('[data-edit-save]');
  if(saveBtn)saveBtn.onclick=()=>saveCaseEdit(c,isPending,rerender,saveBtn);
  if(edit&&!edit.focused){edit.focused=true;root.querySelector('.case-edit-form [data-edit-field]')?.focus({preventScroll:true})} // 只在刚进入编辑时聚焦，重渲染不抢焦点
  const describeBtn=root.querySelector('#case-describe-btn');
  if(describeBtn)describeBtn.onclick=()=>{startCaseEdit(c,isPending,'describe');rerender()};
  const key=simplifyKey(c,isPending),btn=root.querySelector('#case-simplify-btn'),editBtn=root.querySelector('#case-simplify-edit-btn');
  if(btn){btn.dataset.simplifyKey=key;btn.onclick=()=>runSimplify(c,isPending,rerender)}
  if(editBtn){editBtn.dataset.simplifyKey=key;editBtn.onclick=()=>{if(simplifyInFlight.has(key))return;startCaseEdit(c,isPending,'friendly');rerender()}}
  syncSimplifyButtons(key);
}
// 人工修改阅读友好版（页面内编辑，见 saveCaseEdit）复用 simplifyCase/simplifyPendingCase 持久化：服务端同时把
// SimplifiedFrom* 更新为用例当前内容，所以编辑"已过时"的旧版本保存后即不再过时。
// 正在生成阅读友好版的用例：key -> 开始时间。忙碌态记在模块级而不是按钮 DOM 上——
// 生成期间任何重渲染（render()/renderReqFocus()）重新画出的按钮仍是禁用+计时态，
// 结束或超时前都不能再次点击。按钮也不能用 $('#case-simplify-btn') 取：用例管理
// (#detail) 和需求评审 (#req-detail) 两个面板里可能各有一个同 id 的按钮，
// querySelector 会命中隐藏面板里那个，导致可见按钮没有立刻变成禁用态。
const simplifyInFlight=new Map();
const SIMPLIFY_CLIENT_TIMEOUT_MS=75000; // 服务端改写上限 60 秒，额外留出代理/网络余量
const simplifyKey=(c,isPending)=>`${isPending?'pending':c.VersionID}:${c.ID}`;
function syncSimplifyButtons(key){
  const startedAt=simplifyInFlight.get(key);
  if(startedAt===undefined)return;
  const seconds=Math.floor((Date.now()-startedAt)/1000);
  document.querySelectorAll('#case-simplify-btn,#case-simplify-edit-btn').forEach(b=>{
    if(b.dataset.simplifyKey!==key)return;
    b.disabled=true; // 生成结果会覆盖人工修改，生成期间编辑按钮一并锁住
    if(b.id==='case-simplify-btn')b.textContent=seconds?`生成中…（已等待 ${seconds} 秒，最长约 60 秒）`:'生成中…';
  });
}
async function runSimplify(c,isPending,rerender){
  const key=simplifyKey(c,isPending);
  if(simplifyInFlight.has(key))return;
  simplifyInFlight.set(key,Date.now());
  syncSimplifyButtons(key);
  // The rewrite runs a real model call (up to 60s server-side) — a static "生成中…" label looks
  // frozen for that long, so tick elapsed seconds to show it is still working, not stuck.
  const ticker=setInterval(()=>syncSimplifyButtons(key),1000);
  const controller=new AbortController(),abortTimer=setTimeout(()=>controller.abort(),SIMPLIFY_CLIENT_TIMEOUT_MS);
  try{
    let friendly;
    try{
      friendly=await serviceRequest('/api/planner/simplify',{method:'POST',headers:{'Content-Type':'application/json'},signal:controller.signal,
        body:JSON.stringify({title:c.Title||'',preconditions:c.Preconditions||'',steps:c.Steps||'',expected:c.Expected||''})});
    }catch(e){toast(controller.signal.aborted?'生成超时，请稍后重试':`生成失败：${e.message}`,true);return}
    clearTimeout(abortTimer);
    const payload={CaseID:c.ID,SimplifiedPreconditions:friendly.preconditions||'',SimplifiedSteps:friendly.steps||'',SimplifiedExpected:friendly.expected||''};
    if(!isPending)payload.VersionID=c.VersionID;
    if(caseViewFor===key)caseViewMode='friendly'; // 刚生成完，直接给用户看结果
    try{await (isPending?actReview:act)(isPending?'simplifyPendingCase':'simplifyCase',payload);toast('已生成阅读友好版本')}
    catch{}
  }finally{
    clearTimeout(abortTimer);clearInterval(ticker);
    simplifyInFlight.delete(key);
    rerender();
  }
}

function renderFocus(){if(!focus){updateEmptyHint();$('#empty').classList.remove('hidden');$('#detail').classList.add('hidden');return}$('#empty').classList.add('hidden');let d=$('#detail');d.classList.remove('hidden');if(focus.type==='folder'){let f=state.folders.find(x=>x.VersionID===focus.versionID&&x.ID===focus.id);if(!f){focus=null;return renderFocus()}let descendants=descendantFolders(f),count=cases(f.VersionID).filter(c=>c.FolderID===f.ID||descendants.includes(c.FolderID)).length;d.innerHTML=`<div class="detail-head"><div><div class="eyebrow">文件夹 · ${esc(version(f.VersionID).name)}</div><h1>📁 ${esc(f.Name)}</h1></div></div><div class="card meta-grid"><span>用例 <b>${count}</b></span><span>子文件夹 <b>${descendants.length}</b></span>${f.Moved?'<span>状态 <b>已移动 · 未合并到主线</b></span>':''}<span>创建者 <b>${esc(f.CreatedBy)}</b></span><span>创建时间 <b>${fmt(f.CreatedAt)}</b></span></div>`;return}let c=state.cases.find(x=>x.VersionID===focus.versionID&&x.ID===focus.id);if(!c){focus=null;return renderFocus()}let branch=!version(c.VersionID).mainline,h=state.histories.filter(x=>x.CaseID===c.ID&&x.VersionID===c.VersionID).slice().reverse();d.innerHTML=`<div class="detail-head"><div><div class="eyebrow">${esc(c.ID)} · ${esc(version(c.VersionID).name)} ${c.Dirty?'· 未合并':''}</div><h1>${esc(c.Title)}</h1></div><div class="detail-actions">${branch?'<button id="edit-case" class="secondary">编辑</button>':''}<button id="open-record">测试记录</button></div></div><div class="card case-detail"><div class="meta-grid case-meta"><span>优先级 <b>${esc(c.Priority||'未设置')}</b></span><span>当前结果 <b>${resultName(c.Result)}</b></span><span>更新者 <b>${esc(c.UpdatedBy)}</b></span><span>更新时间 <b>${fmt(c.UpdatedAt)}</b></span><span>基线版本 <b>r${c.BaseRevision||c.Revision}</b></span><button type="button" class="link-button version-toggle" id="case-version-toggle" aria-expanded="false"${h.length?'':' disabled'}>用例版本 <b>${esc(version(c.VersionID).name)}</b><small>${h.length?`${h.length} 条编辑历史 ▾`:'暂无编辑历史'}</small></button></div>${caseDetailBodyHTML(c,false)}${h.length?`<div class="history-list hidden" id="case-history-list">${h.map(x=>`<a class="history-row" href="#history=${encodeURIComponent(x.ID)}&amp;version=${encodeURIComponent(c.VersionID)}"><b>${historyAction(x.Action)}</b> · ${esc(x.Author)} <small>${fmt(x.CreatedAt)}${x.SourceVersionID?` · 来源 ${esc(version(x.SourceVersionID)?.name||x.SourceVersionID)}`:''}</small></a>`).join('')}</div>`:''}</div>`;$('#open-record').onclick=()=>openRecords(c);if(h.length)$('#case-version-toggle').onclick=()=>{let hidden=$('#case-history-list').classList.toggle('hidden');$('#case-version-toggle').setAttribute('aria-expanded',String(!hidden))};bindCaseDetailBody(d,c,false,renderFocus)}
function descendantFolders(f){let out=[];function walk(id){state.folders.filter(x=>x.VersionID===f.VersionID&&x.ParentID===id).forEach(x=>{out.push(x.ID);walk(x.ID)})}walk(f.ID);return out}
function toggleSelect(v,id,on){if(!selected.has(v))selected.set(v,new Set());on?selected.get(v).add(id):selected.get(v).delete(id);updateBulk();updateFolderChecks()}
function updateBulk(){let entries=[...selected.entries()].filter(([,s])=>s.size);let n=entries.reduce((x,[,s])=>x+s.size,0);$('#bulk').classList.toggle('hidden',!n);$('#selected-count').textContent=`已选 ${n} 项`;}
function folderMenu(v,f){let branch=!version(v).mainline;if(!branch)return f==='root'?[['搜索',()=>openSearch(v,f)],['创建测试版本',versionModal],['脚本生成',()=>generateForFolder(v,f)],['复制飞书思维导图',()=>exportFeishuMindmap([{versionID:v,folderID:f}])]]:[['搜索',()=>openSearch(v,f)],['脚本生成',()=>generateForFolder(v,f)],['复制飞书思维导图',()=>exportFeishuMindmap([{versionID:v,folderID:f}])]];let items=[['查看详情',()=>setFocus({type:'folder',versionID:v,id:f})],['新建文件夹',()=>folderModal(v,f)],['新增用例',()=>caseModal(null,v,f)],['创建测试任务',()=>taskFromFolder(v,f)],['脚本生成',()=>generateForFolder(v,f)],['重命名空文件夹',()=>renameModal(v,f)],['搜索此目录',()=>openSearch(v,f)],['导出目录',()=>exportCases(v,f)],['复制飞书思维导图',()=>exportFeishuMindmap([{versionID:v,folderID:f}])]];if(f!=='root')items.push(['移动到…',()=>moveFolderModal(v,f)],['合并到主线',()=>mergeFolderModal(v,f)],['删除空文件夹',()=>{if(confirm('删除该空文件夹？'))act('deleteFolder',{VersionID:v,FolderID:f})}]);return items}
function folderOptionsHTML(versionID,exclude=''){let fs=folders(versionID),roots=fs.filter(f=>!f.ParentID||!fs.some(x=>x.ID===f.ParentID));function node(f,depth){if(f.ID===exclude)return '';let children=fs.filter(x=>x.ParentID===f.ID);return `<option value="${f.ID}">${'　'.repeat(depth)}${esc(f.Name)}</option>`+children.map(ch=>node(ch,depth+1)).join('')}return roots.map(r=>node(r,0)).join('')}
// 移动文件夹：目标下拉里去掉自身及其子文件夹。测试版本中的移动在合并到主线时同步到主线。
function moveFolderModal(v,f){const folder=state.folders.find(x=>x.VersionID===v&&x.ID===f);showModal('移动文件夹',`<p class="meta">将「${esc(folder?.Name||'')}」连同其中的子文件夹和用例移动到：</p><label>目标文件夹<select name="TargetFolderID">${folderOptionsHTML(v,f)}</select></label>`,x=>act('moveFolder',{VersionID:v,FolderID:f,TargetFolderID:x.TargetFolderID}))}
function targetFolderModal(title,versionID,onSubmit){showModal(title,`<label>目标文件夹<select name="TargetFolderID">${folderOptionsHTML(versionID)}</select></label>`,x=>onSubmit(x.TargetFolderID))}
function mergeFolderModal(v,f){targetFolderModal('合并到主线','main',id=>act('mergeFolder',{VersionID:v,FolderID:f,TargetFolderID:id}))}
function mergeCasesModal(v,ids){targetFolderModal('合并到主线','main',id=>act('mergeCases',{VersionID:v,CaseIDs:ids,TargetFolderID:id}))}
function moveCasesModal(v,ids){targetFolderModal('移动用例',v,id=>act('moveCases',{VersionID:v,CaseIDs:ids,TargetFolderID:id}))}
function caseMenu(v,id){let c=state.cases.find(x=>x.VersionID===v&&x.ID===id),items=[['查看详情',()=>setFocus({type:'case',versionID:v,id})],['测试记录',()=>openRecords(c)],['脚本生成',()=>startGeneration(v,[id],caseLabel(c))]];if(!version(v).mainline)items.push(['编辑用例',()=>{startCaseEdit(c,false,'case');setFocus({type:'case',versionID:v,id})}],['删除用例',()=>{if(confirm('确定删除这条用例？测试记录和历史也会一并删除，且无法恢复。'))act('deleteCases',{VersionID:v,CaseIDs:[id]})}]);return items}
function menu(e,items){e.preventDefault();let m=$('#context-menu');m.innerHTML=items.map((x,i)=>`<button data-i="${i}"${x[2]?` class="${x[2]}"`:''}>${esc(x[0])}</button>`).join('');m.style.left=Math.min(e.clientX,innerWidth-205)+'px';m.style.top=Math.min(e.clientY,innerHeight-items.length*38-10)+'px';m.classList.remove('hidden');m.querySelectorAll('button').forEach(b=>b.onclick=()=>{m.classList.add('hidden');items[+b.dataset.i][1]()})}
function showModal(title,html,save){$('#modal-title').textContent=title;$('#modal-body').innerHTML=html;modalSave=save;$('#modal').showModal()}
function versionModal(){showModal('创建测试版本',`<label>版本名称<input name="Name" required placeholder="例如：v2.4.0 回归"></label>`,x=>act('createVersion',x))}
function folderModal(v,parent){showModal('新建文件夹',`<label>文件夹名称<input name="Name" required></label>`,x=>act('createFolder',{...x,VersionID:v,ParentID:parent}))}
function renameModal(v,id){let f=state.folders.find(x=>x.VersionID===v&&x.ID===id);showModal('重命名文件夹',`<label>文件夹名称<input name="Name" required value="${esc(f.Name)}"></label>`,x=>act('renameFolder',{...x,VersionID:v,FolderID:id}))}
function caseModal(c,v,f){v=c?.VersionID||v;f=c?.FolderID||f;showModal(c?'编辑测试用例':'新增测试用例',`<label>标题<input name="Title" required value="${esc(c?.Title||'')}"></label><label>优先级<select name="Priority">${['P0','P1','P2','P3'].map(x=>`<option ${c?.Priority===x?'selected':''}>${x}</option>`).join('')}</select></label><label>用例描述（评审时人工填写，可选）<textarea name="Description">${esc(c?.Description||'')}</textarea></label><label>前置条件<textarea name="Preconditions">${esc(c?.Preconditions||'')}</textarea></label><label>执行步骤<textarea name="Steps">${esc(c?.Steps||'')}</textarea></label><label>预期结果<textarea name="Expected">${esc(c?.Expected||'')}</textarea></label>`,x=>act(c?'editCase':'createCase',{...x,VersionID:v,FolderID:f,CaseID:c?.ID||''}))}
function taskFromFolder(v,f){let ids=cases(v).filter(c=>c.FolderID===f||descendantFolders(state.folders.find(x=>x.VersionID===v&&x.ID===f)).includes(c.FolderID)).map(c=>c.ID);if(!ids.length)return toast('该目录没有用例',true);taskModal(v,ids)}
function taskModal(v,ids){showModal('创建测试任务',`<label>任务名称<input name="Name" required placeholder="例如：登录模块冒烟测试"></label><p class="meta">包含 ${ids.length} 条用例</p>`,x=>act('createTask',{...x,VersionID:v,CaseIDs:ids}))}
// 用例描述是评审阶段给人看的速览，不进入任何导出（JSON 导出、复制飞书思维导图）。
const exportableCase=({Description,...c})=>c;
function exportCases(v,f){let subs=f?[f,...descendantFolders(state.folders.find(x=>x.VersionID===v&&x.ID===f))]:folders(v).map(x=>x.ID),data=cases(v).filter(c=>subs.includes(c.FolderID)).map(exportableCase);let a=document.createElement('a');a.href=URL.createObjectURL(new Blob([JSON.stringify(data,null,2)],{type:'application/json'}));a.download=`casehub-${version(v).name}.json`;a.click();URL.revokeObjectURL(a.href)}
function openSearch(v='',f=''){let p=$('#search-panel');p.dataset.version=v;p.dataset.folder=f;p.classList.remove('hidden');$('#workspace').style.gridTemplateColumns=`var(--side) 5px 370px minmax(0,1fr)`;$('#search-query').focus()}
function runSearch(){let q=$('#search-query').value.trim().toLowerCase(),field=$('#search-field').value,result=$('#search-result').value,v=$('#search-panel').dataset.version,f=$('#search-panel').dataset.folder,list=state.cases.filter(c=>(!v||c.VersionID===v));if(f){let fs=[f,...descendantFolders(state.folders.find(x=>x.VersionID===v&&x.ID===f))];list=list.filter(c=>fs.includes(c.FolderID))}list=list.filter(c=>{if(!q)return true;if(field!=='all')return String(c[field]||'').toLowerCase().includes(q);return [c.Title,c.ID,c.Description,c.Preconditions,c.Steps,c.Expected].some(x=>String(x||'').toLowerCase().includes(q))});if(result)list=list.filter(c=>result==='none'?!c.Result:c.Result===result);let box=$('#search-results');if($('#search-mode').value==='tree'&&v){let ids=new Set(list.map(x=>x.ID));box.innerHTML=tree(v,ids);bindTree(box)}else{box.innerHTML=list.map(c=>{let branch=!version(c.VersionID).mainline,checked=selected.get(c.VersionID)?.has(c.ID);return `<div class="search-hit" data-v="${c.VersionID}" data-c="${c.ID}">${branch?`<input class="case-check" type="checkbox" ${checked?'checked':''}>`:''}<div class="search-hit-body"><b>${esc(c.Title)}</b>${c.Description?`<small class="search-hit-desc">${esc(c.Description)}</small>`:''}<small>${esc(c.ID)} · ${esc(version(c.VersionID).name)}</small></div></div>`}).join('')||'<p class="meta">没有匹配结果</p>';box.querySelectorAll('.search-hit').forEach(e=>e.onclick=x=>{if(x.target.matches('input')){toggleSelect(e.dataset.v,e.dataset.c,x.target.checked);return}setFocus({type:'case',versionID:e.dataset.v,id:e.dataset.c})})}}
// 测试任务界面：不还原原有目录结构。每个版本一个文件夹（名称=版本名），
// 版本文件夹下只有一级子文件夹，每个子文件夹对应一个测试任务（名称=创建时填写的任务名称），
// 任务内的用例平铺展示，不保留其在版本目录树中的原始路径。
function taskHTML(t){
  let cs=t.CaseIDs.map(id=>state.cases.find(c=>c.VersionID===t.VersionID&&c.ID===id)).filter(Boolean);
  return `<div class="task" data-task="${t.ID}"><div class="task-head" data-task="${t.ID}">▸ ${esc(t.Name)} <small>(${t.CaseIDs.length})</small></div><div class="task-body${openTasks.has(t.ID)?'':' hidden'}">${cs.map(c=>caseRow(c,1,false,t.ID)).join('')||'<p class="meta" style="padding:8px 15px">空任务</p>'}</div></div>`;
}
function renderTasks(){
  let box=$('#tasks');
  let byVersion=new Map();
  for(const t of state.tasks){
    if(!byVersion.has(t.VersionID))byVersion.set(t.VersionID,[]);
    byVersion.get(t.VersionID).push(t);
  }
  box.innerHTML=[...byVersion.entries()].map(([vid,tasks])=>{
    let vName=version(vid)?.name||'(版本已删除)',closed=closedTaskVersions.has(vid);
    return `<div class="tree-row folder-row${closed?' closed':''}" data-task-version="${vid}"><span class="chev">▾</span><span>📁</span><span class="label">${esc(vName)}</span></div><div>${tasks.map(t=>taskHTML(t)).join('')}</div>`;
  }).join('')||'<p class="meta" style="padding:15px">暂无测试任务</p>';
  box.querySelectorAll('[data-task-version]').forEach(e=>{e.querySelector('.chev').onclick=x=>{x.stopPropagation();let closed=e.classList.toggle('closed');closed?closedTaskVersions.add(e.dataset.taskVersion):closedTaskVersions.delete(e.dataset.taskVersion)}});
  box.querySelectorAll('.task-head').forEach(e=>e.onclick=()=>{let id=e.dataset.task,hidden=e.nextElementSibling.classList.toggle('hidden');hidden?openTasks.delete(id):openTasks.add(id)});
  box.querySelectorAll('.task-case').forEach(e=>{e.onclick=()=>setFocus({type:'case',versionID:e.dataset.v,id:e.dataset.c,taskID:e.dataset.task});e.oncontextmenu=x=>menu(x,taskCaseMenu(e.dataset.v,e.dataset.c,e.dataset.task))});
}
function taskCaseMenu(v,id,taskID){let c=state.cases.find(x=>x.VersionID===v&&x.ID===id);return [['查看详情',()=>setFocus({type:'case',versionID:v,id,taskID})],['测试记录',()=>openRecords(c)],['标记通过',()=>markResult(v,id,taskID,'passed')],['标记失败',()=>markResult(v,id,taskID,'failed')],['标记阻塞',()=>markResult(v,id,taskID,'blocked')]]}
function markResult(v,id,taskID,result){return act('submitRecord',{Result:result,Note:'',VersionID:v,CaseID:id,TaskID:taskID}).then(()=>toast(`已标记为${resultName(result)}`))}
function openRecords(c){recordCase=c;recordHistoryLimit=3;$('#record-history').replaceChildren();$('#record-form').reset();$('#drawer').classList.remove('hidden');resetRecordEditor();let branches=state.versions.filter(v=>!v.mainline&&state.cases.some(x=>x.VersionID===v.id&&x.ID===c.ID));$('#record-version').innerHTML=(version(c.VersionID).mainline?[version(c.VersionID),...branches]:[version(c.VersionID)]).map(v=>`<option value="${v.id}">${esc(v.name)}</option>`).join('');$('#record-version').closest('label').classList.toggle('hidden',!version(c.VersionID).mainline);$('#drawer-case').textContent=`${c.ID} · ${c.Title}`;$('#drawer-case-desc').innerHTML=caseDescriptionHTML(c,false);renderRecordHistory();$('#drawer').classList.remove('hidden')}
function renderRecordHistory(){
  const box=$('#record-history'),opened=new Set([...box.querySelectorAll('.record[open]')].map(el=>el.dataset.id));
  recordViewers.forEach(v=>v.destroy());recordViewers=[];
  const vid=$('#record-version').value||recordCase.VersionID;
  const readOnly=vid==='main';
  $$('#record-form > :not(label:first-child)').forEach(el=>el.classList.toggle('hidden',readOnly));
  const records=state.records.filter(r=>r.VersionID===vid&&r.CaseID===recordCase.ID).slice().reverse();
  if(!records.length){box.innerHTML='<p class="meta">暂无测试记录</p>';return}
  box.innerHTML=`<div class="record-history-heading"><strong>历史记录</strong><small>共 ${records.length} 条</small></div>`+records.slice(0,recordHistoryLimit).map(r=>{
    const preview=(r.Note||'无备注').replace(/\s+/g,' ').trim();
    return `<details class="record" data-id="${esc(r.ID)}" ${opened.has(r.ID)?'open':''}><summary><span class="record-summary-title"><b>${resultName(r.Result)}</b> · ${r.Submitted?'已提交':'草稿'}</span><small>${esc(r.Author)} · ${fmt(r.CreatedAt)}${r.SourceVersionID?` · 来源 ${esc(r.SourceVersionName||r.SourceVersionID)}`:''}</small><span class="record-preview">${esc(preview.slice(0,80))}${preview.length>80?'…':''}</span></summary><div class="record-note"></div></details>`;
  }).join('')+(records.length>3?`<button type="button" class="secondary record-history-toggle" id="toggle-record-history" aria-expanded="${recordHistoryLimit>3}">${recordHistoryLimit>3?'收起较早记录':`显示全部 ${records.length} 条记录`}</button>`:'');
  box.querySelectorAll('.record').forEach(details=>{
    let viewer=null;
    const show=()=>{
      if(!details.open||viewer||!details.isConnected)return;
      const r=records.find(r=>r.ID===details.dataset.id),el=details.querySelector('.record-note');
      el.classList.toggle('toastui-editor-dark',document.documentElement.classList.contains('dark'));
      viewer=toastui.Editor.factory({el,viewer:true,initialValue:r.Note||'无备注',usageStatistics:false});
      recordViewers.push(viewer);
    };
    details.addEventListener('toggle',show);show();
  });
  const toggle=$('#toggle-record-history');
  if(toggle)toggle.onclick=()=>{recordHistoryLimit=recordHistoryLimit>3?3:Infinity;renderRecordHistory()};
}

async function saveRecord(type){
  const editor=recordEditor,x=Object.fromEntries(new FormData($('#record-form'))),vid=$('#record-version').value||recordCase.VersionID;
  if(!editor||$('#save-draft').disabled)return;
  const note=editor.getMarkdown(),buttons=$$('#record-form .drawer-actions button');
  buttons.forEach(b=>b.disabled=true);
  try{
    await act(type,{Result:x.result,Note:note,VersionID:vid,CaseID:recordCase.ID,TaskID:recordTask});
    if(recordEditor===editor){renderRecordHistory();if(type==='submitRecord'&&editor.getMarkdown()===note)editor.setMarkdown('')}
    toast(type==='submitRecord'?'测试记录已提交':'草稿已保存');
  }finally{buttons.forEach(b=>b.disabled=false)}
}
function resultName(x){return({passed:'通过',failed:'失败',blocked:'阻塞'}[x]||'未执行')}
function toast(msg,bad=false){let t=$('#toast');t.textContent=msg;t.style.background=bad?'#991b1b':'#172033';t.classList.remove('hidden');clearTimeout(t._timer);t._timer=setTimeout(()=>t.classList.add('hidden'),3200)}
$('#create-version').onclick=versionModal;$('#theme').onclick=()=>{document.documentElement.classList.toggle('dark');localStorage.setItem('casehub-theme',document.documentElement.classList.contains('dark')?'dark':'light');if(recordEditor){$('#record-editor').classList.toggle('toastui-editor-dark',document.documentElement.classList.contains('dark'));renderRecordHistory()}if(reqEditor)$('#req-editor').classList.toggle('toastui-editor-dark',document.documentElement.classList.contains('dark'))};$('#collapse').onclick=()=>setSidebarCollapsed(true);$('#expand').onclick=$('#empty-expand').onclick=()=>setSidebarCollapsed(false);
$$('[data-view]').forEach(b=>b.onclick=()=>{$$('[data-view]').forEach(x=>x.classList.toggle('active',x===b));view=b.dataset.view;$('#case-view').classList.toggle('hidden',view!=='cases');$('#task-view').classList.toggle('hidden',view!=='tasks');updateEmptyHint()});document.addEventListener('click',e=>{if(!e.target.closest('#context-menu'))$('#context-menu').classList.add('hidden')});
$('#modal-close').onclick=$('#modal-cancel').onclick=()=>$('#modal').close();$('#modal-form').onsubmit=async e=>{e.preventDefault();try{await modalSave(Object.fromEntries(new FormData(e.target)));$('#modal').close()}catch{}};$('#drawer-close').onclick=closeRecordDrawer;$('#record-version').onchange=()=>{recordHistoryLimit=3;$('#record-history').replaceChildren();renderRecordHistory()};$('#record-form').onsubmit=async e=>{e.preventDefault();try{await saveRecord('submitRecord')}catch{}};$('#save-draft').onclick=()=>saveRecord('saveRecord').catch(()=>{});$('#close-search').onclick=()=>{$('#search-panel').classList.add('hidden');$('#workspace').style.gridTemplateColumns='var(--side) 5px minmax(0,1fr)'};$('#run-search').onclick=runSearch;
$$('[data-bulk]').forEach(b=>b.onclick=()=>{if(b.dataset.bulk==='feishu'){exportFeishuMindmap([...selected.entries()].filter(([,ids])=>ids.size).map(([versionID,ids])=>({versionID,ids:[...ids]})));return}let entries=[...selected.entries()].find(([,s])=>s.size);if(!entries)return;let[v,ids]=[entries[0],[...entries[1]]];if(b.dataset.bulk==='export')exportSelected(v,ids);else if(b.dataset.bulk==='task')taskModal(v,ids);else if(b.dataset.bulk==='script')startGeneration(v,ids,`已选 ${ids.length} 条用例`);else if(b.dataset.bulk==='move')moveCasesModal(v,ids);else if(b.dataset.bulk==='merge')mergeCasesModal(v,ids);else if(b.dataset.bulk==='delete'){if(confirm(`确定删除选中的 ${ids.length} 条用例？测试记录和历史也会一并删除，且无法恢复。`))act('deleteCases',{VersionID:v,CaseIDs:ids}).then(()=>{selected.delete(v);updateBulk()}).catch(()=>{})}});function exportSelected(v,ids){let data=cases(v).filter(c=>ids.includes(c.ID)).map(exportableCase),a=document.createElement('a');a.href=URL.createObjectURL(new Blob([JSON.stringify(data,null,2)],{type:'application/json'}));a.download='casehub-selected.json';a.click()}
let resizing=false,lastX=340,resizeVar='--side',resizeCollapse='#collapse';
function bindResizer(handleSel,collapseSel,cssVar){$(handleSel).onmousedown=()=>{resizing=true;resizeVar=cssVar;resizeCollapse=collapseSel;$(handleSel).classList.add('dragging')}}
bindResizer('#resize-left','#collapse','--side');
bindResizer('#req-resize-left','#req-collapse','--req-side');
bindResizer('#auto-resize-left','#auto-collapse','--auto-side');
document.onmousemove=e=>{if(resizing){lastX=e.clientX;document.documentElement.style.setProperty(resizeVar,Math.max(0,Math.min(600,e.clientX))+'px')}};
document.onmouseup=()=>{if(resizing&&lastX<70)$(resizeCollapse).click();else if(resizing&&lastX<220)document.documentElement.style.setProperty(resizeVar,'220px');resizing=false;$$('.resizer').forEach(r=>r.classList.remove('dragging'))};
document.documentElement.classList.toggle('dark',localStorage.getItem('casehub-theme')==='dark');
window.addEventListener('hashchange',renderHistoryRoute);
refresh().then(resumeGenTasks).catch(e=>toast(e.message,true));

function resetRecordEditor(initialValue='') {
  if(recordEditor){recordEditor.setMarkdown(initialValue);return}
  recordEditor=new toastui.Editor({el:$('#record-editor'),height:'360px',initialEditType:'wysiwyg',previewStyle:'tab',initialValue,language:'zh-CN',theme:document.documentElement.classList.contains('dark')?'dark':'light',usageStatistics:false,autofocus:false});
}
function historyAction(action){return {create:'新增',edit:'编辑',merge:'合并',move:'移动'}[action]||action}
function renderHistoryRoute(){
  if(!state)return;
  const params=new URLSearchParams(location.hash.slice(1)),id=params.get('history');
  if(!id){renderFocus();return}
  const h=state.histories.find(x=>x.ID===id),d=$('#detail');
  $('#empty').classList.add('hidden');d.classList.remove('hidden');
  if(!h){d.innerHTML='<a href="#">← 返回用例详情</a><p>该编辑历史不存在。</p>';return}
  focus={type:'case',versionID:params.get('version')||h.VersionID,id:h.CaseID};
  const fields=[['Title','标题'],['Priority','优先级'],['Description','用例描述'],['Preconditions','前置条件'],['Steps','执行步骤'],['Expected','预期结果']];
  d.innerHTML=`<a href="#">← 返回用例详情</a><div class="detail-head"><div><div class="eyebrow">${esc(h.CaseID)} · ${esc(version(h.VersionID)?.name||h.VersionID)}</div><h1>${historyAction(h.Action)}历史</h1><p class="meta">${esc(h.Author)} · ${fmt(h.CreatedAt)} · 来源 ${esc(version(h.SourceVersionID)?.name||h.SourceVersionID||'—')}</p></div></div><div class="card history-snapshot"><table><thead><tr><th>字段</th><th>变更前${h.Before?'':'（无快照）'}</th><th>变更后</th></tr></thead><tbody>${fields.map(([key,label])=>`<tr class="${h.Before&&h.Before[key]!==h.After?.[key]?'changed':''}"><th>${label}</th><td>${esc(h.Before?.[key]??'—')}</td><td>${esc(h.After?.[key]??'—')}</td></tr>`).join('')}</tbody></table></div>`;
}

function updateEmptyHint(){
  const collapsed=$('#sidebar').classList.contains('hidden'),tasks=view==='tasks';
  $('#empty-title').textContent=collapsed?'展开侧栏，继续浏览':tasks?'选择一个测试任务':'从一个用例开始';
  $('#empty-hint').textContent=collapsed?'还没有选择要查看的内容。展开侧栏后，选择文件夹或用例即可查看详情。':tasks?'展开左侧的测试任务，选择其中的用例查看详情。':'选择左侧的文件夹或用例，即可在这里查看详情。';
  $('#empty-expand').textContent=tasks?'展开测试任务':'展开用例树';
  $('#empty-expand').classList.toggle('hidden',!collapsed);
}
let sidebarWidth=null;
function setSidebarCollapsed(collapsed){
  if(collapsed&&!$('#sidebar').classList.contains('hidden')){
    const width=$('#sidebar').getBoundingClientRect().width;
    if(width>=220)sidebarWidth=width;
  }
  document.documentElement.style.setProperty('--side',collapsed?'0px':`${sidebarWidth||340}px`);
  $('#workspace').classList.toggle('sidebar-collapsed',collapsed);
  $('#sidebar').classList.toggle('hidden',collapsed);
  $('#resize-left').classList.toggle('hidden',collapsed);
  $('#expand').classList.toggle('hidden',!collapsed);
  updateEmptyHint();
}

// ---- Requirement management page ----
const reqFolder=id=>state.reqFolders.find(f=>f.ID===id);
const reqChildren=id=>state.reqFolders.filter(f=>f.ParentID===id);
const reqDocsIn=id=>state.reqDocs.filter(d=>d.FolderID===id);
function reqRoots(){return state.reqFolders.filter(f=>!f.ParentID||!state.reqFolders.some(x=>x.ID===f.ParentID))}
function reqDescendantFolders(f){let out=[];function walk(id){reqChildren(id).forEach(x=>{out.push(x.ID);walk(x.ID)})}walk(f.ID);return out}
function reqTreeHTML(){
  function node(f,depth){
    let children=reqChildren(f.ID),own=reqDocsIn(f.ID);
    return `<div class="tree-row folder-row" style="padding-left:${8+depth*17}px" data-req-folder="${f.ID}"><span class="chev">▾</span><span>📁</span><span class="label">${esc(f.Name)}</span></div><div>${own.map(d=>reqDocRow(d,depth+1)).join('')}${children.map(ch=>node(ch,depth+1)).join('')}</div>`;
  }
  return reqRoots().map(r=>node(r,0)).join('')||'<p class="meta">暂无需求文档</p>';
}
function reqDocRow(d,depth){return `<div class="tree-row case-row" style="padding-left:${8+depth*17}px" data-req-doc="${d.ID}"><span class="label">📄 ${esc(d.Title)}</span></div>`}
// ---- Requirement cross-linking ("引用需求") ----
// A doc references another via an ordinary markdown link with a `req:` href
// (e.g. `[REQ-002 权限管理](req:REQ-002)`), inserted through the 🔗 引用需求
// picker below or written by hand. Referenced docs are background/context for
// the doc being designed — informational only, never a source of their own
// test cases — so the AI 设计 payload (see renderAiForm) folds their content
// into context.instructions rather than adding them to requirements[].
function reqRefs(content){const out=[],seen=new Set();const re=/\]\(req:([^)\s]+)\)/g;let m;while(m=re.exec(content||'')){if(!seen.has(m[1])){seen.add(m[1]);out.push(m[1])}}return out}
function reqBacklinks(id){return state.reqDocs.filter(d=>d.ID!==id&&reqRefs(d.Content).includes(id))}
// One level of docs `doc` references, resolved to their current title/content.
function reqRefDocs(doc){
  const seen=new Set([doc.ID]),out=[];
  for(const id of reqRefs(doc.Content)){
    if(seen.has(id))continue;
    const d=state.reqDocs.find(x=>x.ID===id);
    if(!d)continue;
    seen.add(id);out.push(d);
  }
  return out;
}
// Referenced docs rendered as labelled background text for context.instructions —
// clearly marked as reference-only so the planner never designs cases against them.
function reqRefContext(doc){
  const refs=reqRefDocs(doc);
  if(!refs.length)return '';
  return refs.map(d=>`【关联需求 ${d.ID} ${d.Title} —— 仅供背景参考，不要据此设计独立用例】\n${d.Content||''}`).join('\n\n');
}
const REQ_LINK_SEARCH_THRESHOLD=7;
function openReqLinkPicker(e,doc){
  e.stopPropagation();
  const others=state.reqDocs.filter(d=>d.ID!==doc.ID);
  const showSearch=others.length>REQ_LINK_SEARCH_THRESHOLD;
  const m=$('#context-menu');
  m.innerHTML=(showSearch?'<input id="req-link-search" placeholder="搜索需求 id / 标题…">':'')+'<div id="req-link-list" class="req-link-list"></div>';
  m.style.left=Math.min(e.clientX,innerWidth-245)+'px';
  m.style.top=Math.min(e.clientY,innerHeight-320)+'px';
  m.classList.remove('hidden');
  function renderItems(q){
    const list=$('#req-link-list'),query=(q||'').trim().toLowerCase();
    if(!others.length){list.innerHTML='<p class="meta req-link-empty">项目里没有其他需求文档，先新增一份再引用。</p>';return}
    const matches=!query?others:others.filter(d=>`${d.ID} ${d.Title}`.toLowerCase().includes(query));
    if(!matches.length){list.innerHTML='<p class="meta req-link-empty">没有匹配的需求文档。</p>';return}
    list.innerHTML=matches.map(d=>`<button type="button" data-id="${esc(d.ID)}"><span class="req-link-id">${esc(d.ID)}</span>${esc(d.Title)}</button>`).join('');
    list.querySelectorAll('button').forEach(b=>b.onclick=()=>{insertReqLink(state.reqDocs.find(d=>d.ID===b.dataset.id));m.classList.add('hidden')});
  }
  renderItems('');
  if(showSearch){const inp=$('#req-link-search');inp.oninput=()=>renderItems(inp.value);inp.focus()}
}
function insertReqLink(target){
  if(!target||!reqEditor)return;
  const label=`${target.ID} ${target.Title}`;
  reqEditor.exec('addLink',{linkUrl:`req:${target.ID}`,linkText:label});
  toast(`已插入「${label}」的引用，记得保存`);
}
function renderReqTree(){const box=$('#req-tree');box.innerHTML=reqTreeHTML();bindReqTree(box)}
function bindReqTree(root){
  root.querySelectorAll('[data-req-folder]').forEach(e=>{let key=e.dataset.reqFolder;if(closedReqFolders.has(key))e.classList.add('closed');e.querySelector('.chev').onclick=x=>{x.stopPropagation();let closed=e.classList.toggle('closed');closed?closedReqFolders.add(key):closedReqFolders.delete(key)};e.onclick=()=>setReqFocus({type:'folder',id:e.dataset.reqFolder});e.oncontextmenu=x=>menu(x,reqFolderMenu(e.dataset.reqFolder))});
  root.querySelectorAll('[data-req-doc]').forEach(e=>{e.onclick=()=>setReqFocus({type:'doc',id:e.dataset.reqDoc});e.oncontextmenu=x=>menu(x,reqDocMenu(e.dataset.reqDoc))});
}
function setReqFocus(x){reqFocus=x;renderReqFocus()}
function renderReqFocus(){
  if(!reqFocus){updateReqEmptyHint();$('#req-empty').classList.remove('hidden');$('#req-detail').classList.add('hidden');return}
  $('#req-empty').classList.add('hidden');
  const d=$('#req-detail');d.classList.remove('hidden');
  if(reqFocus.type==='folder'){
    const f=reqFolder(reqFocus.id);
    if(!f){reqFocus=null;return renderReqFocus()}
    reqEditor?.destroy();reqEditor=null;
    const descendants=reqDescendantFolders(f),count=state.reqDocs.filter(x=>x.FolderID===f.ID||descendants.includes(x.FolderID)).length;
    d.innerHTML=`<div class="detail-head"><div><div class="eyebrow">需求文件夹</div><h1>📁 ${esc(f.Name)}</h1></div></div><div class="card meta-grid"><span>需求文档 <b>${count}</b></span><span>子文件夹 <b>${descendants.length}</b></span><span>创建者 <b>${esc(f.CreatedBy)}</b></span><span>创建时间 <b>${fmt(f.CreatedAt)}</b></span></div>`;
    return;
  }
  if(reqFocus.type==='reviewFolder'){
    const f=pendingFolder(reqFocus.id);
    if(!f){reqFocus=null;return renderReqFocus()}
    reqEditor?.destroy();reqEditor=null;
    const descendants=pendingDescendantFolders(f),count=state.pendingCases.filter(x=>x.FolderID===f.ID||descendants.includes(x.FolderID)).length;
    d.innerHTML=`<div class="detail-head"><div><div class="eyebrow">待评审文件夹</div><h1>📁 ${esc(f.Name)}</h1></div></div><div class="card meta-grid">${f.Code?`<span>编号前缀 <b>TC-${esc(f.Code)}</b></span>`:''}<span>用例 <b>${count}</b></span><span>子文件夹 <b>${descendants.length}</b></span><span>创建者 <b>${esc(f.CreatedBy)}</b></span><span>创建时间 <b>${fmt(f.CreatedAt)}</b></span></div>`;
    return;
  }
  if(reqFocus.type==='reviewCase'){
    const c=state.pendingCases.find(x=>x.ID===reqFocus.id);
    if(!c){reqFocus=null;return renderReqFocus()}
    reqEditor?.destroy();reqEditor=null;
    const reviewLabel=({passed:'已通过',rejected:'未通过'})[c.Review]||'待评审';
    d.innerHTML=`<div class="detail-head"><div><div class="eyebrow">${esc(c.ID)} · <span class="badge">${reviewLabel}</span></div><h1>${esc(c.Title)}</h1></div><div class="detail-actions"><button id="edit-review-case" class="secondary">编辑</button><button id="delete-review-case" class="secondary">删除</button><button id="reject-review-case" class="secondary">评审不通过</button><button id="approve-review-case">评审通过</button><button id="import-review-case" class="secondary">导入到版本…</button></div></div><div class="card case-detail"><div class="meta-grid case-meta"><span>优先级 <b>${esc(c.Priority||'未设置')}</b></span><span>评审状态 <b>${reviewLabel}</b></span><span>创建者 <b>${esc(c.CreatedBy)}</b></span><span>更新者 <b>${esc(c.UpdatedBy)}</b></span><span>更新时间 <b>${fmt(c.UpdatedAt)}</b></span>${c.ReviewedBy?`<span>评审人 <b>${esc(c.ReviewedBy)}</b></span><span>评审时间 <b>${fmt(c.ReviewedAt)}</b></span>`:''}</div>${caseDetailBodyHTML(c,true)}</div>`;
    $('#delete-review-case').onclick=()=>{if(confirm('确定删除这条待评审用例？'))actReview('deletePendingCase',{CaseID:c.ID})};
    $('#approve-review-case').onclick=()=>actReview('reviewPendingCase',{CaseID:c.ID,Review:'passed'});
    $('#reject-review-case').onclick=()=>actReview('reviewPendingCase',{CaseID:c.ID,Review:'rejected'});
    $('#import-review-case').onclick=()=>importReviewModal([c.ID],c.FolderID);
    bindCaseDetailBody(d,c,true,renderReqFocus);
    return;
  }
  const doc=state.reqDocs.find(x=>x.ID===reqFocus.id);
  if(!doc){reqFocus=null;return renderReqFocus()}
  const outRefs=reqRefs(doc.Content).map(id=>state.reqDocs.find(x=>x.ID===id)).filter(Boolean);
  const inRefs=reqBacklinks(doc.ID);
  const relHTML=(outRefs.length||inRefs.length)?`<div class="card req-rel"><h4>关联需求</h4><div class="req-rel-list">${outRefs.map(r=>`<button type="button" class="req-rel-chip" data-req-doc="${esc(r.ID)}">→ ${esc(r.ID)} ${esc(r.Title)}</button>`).join('')}${inRefs.map(r=>`<button type="button" class="req-rel-chip" data-req-doc="${esc(r.ID)}">← ${esc(r.ID)} ${esc(r.Title)}</button>`).join('')}</div></div>`:'';
  d.innerHTML=`<div class="detail-head"><div><div class="eyebrow">${esc(doc.ID)}</div><h1>${esc(doc.Title)}</h1></div><div class="detail-actions"><button type="button" id="req-link-btn" class="secondary">🔗 引用需求</button><button type="button" id="req-ai-design" class="secondary${isAiActive(doc)?' ai-running':''}">${isAiActive(doc)?'AI 设计中':'AI 设计'}</button><button type="button" id="save-req-doc">保存</button></div></div><div class="card meta-grid"><span>需求缩写 <b id="req-doc-code">${esc(doc.Code||'未设置')}</b></span><span>创建者 <b>${esc(doc.CreatedBy)}</b></span><span>创建时间 <b>${fmt(doc.CreatedAt)}</b></span><span>更新者 <b>${esc(doc.UpdatedBy)}</b></span><span>更新时间 <b>${fmt(doc.UpdatedAt)}</b></span></div>${relHTML}<div class="card"><div id="req-editor"></div></div>`;
  reqEditor?.destroy();
  reqEditor=new toastui.Editor({el:$('#req-editor'),height:'520px',initialEditType:'wysiwyg',previewStyle:'tab',initialValue:doc.Content||'',language:'zh-CN',theme:document.documentElement.classList.contains('dark')?'dark':'light',usageStatistics:false});
  $('#save-req-doc').onclick=()=>saveReqDoc(doc).catch(()=>{});
  $('#req-ai-design').onclick=()=>openAiDrawer(doc);
  $('#req-link-btn').onclick=e=>openReqLinkPicker(e,doc);
  d.querySelectorAll('.req-rel-chip').forEach(b=>b.onclick=()=>setReqFocus({type:'doc',id:b.dataset.reqDoc}));
  if(localStorage.getItem(aiJobKey(doc))&&!isAiActive(doc))checkAiJob(doc).catch(()=>{});
}
async function saveReqDoc(doc){
  await act('editReqDoc',{DocID:doc.ID,Title:doc.Title,Content:reqEditor.getMarkdown()});
  renderReqFocus();
  toast('需求文档已保存');
}
function currentReqFolderTarget(){
  if(reqFocus?.type==='folder')return reqFocus.id;
  if(reqFocus?.type==='doc'){const d=state.reqDocs.find(x=>x.ID===reqFocus.id);if(d)return d.FolderID}
  return reqRoots()[0]?.ID||'';
}
$('#create-req-folder').onclick=()=>reqFolderModal(currentReqFolderTarget());
$('#create-req-doc').onclick=()=>reqDocModal(currentReqFolderTarget());
function reqFolderMenu(id){const items=[['查看详情',()=>setReqFocus({type:'folder',id})],['新建文件夹',()=>reqFolderModal(id)],['新增需求文档',()=>reqDocModal(id)],['重命名文件夹',()=>renameReqFolderModal(id)]];if(id!=='req-root')items.push(['删除空文件夹',()=>{if(confirm('确定删除这个空文件夹？'))actReview('deleteReqFolder',{FolderID:id})}]);return items}
function reqDocMenu(id){const doc=state.reqDocs.find(x=>x.ID===id),active=isAiActive(doc);return [['查看详情',()=>setReqFocus({type:'doc',id})],['重命名',()=>renameReqDocModal(doc)],[active?'AI 设计中':'AI 设计',()=>openAiDrawer(doc),active?'ai-running':''],['删除',()=>{if(confirm('确定删除这份需求文档？'))actReview('deleteReqDoc',{DocID:id})}]]}
function reqFolderModal(parent){showModal('新建文件夹',`<label>文件夹名称<input name="Name" required></label>`,x=>act('createReqFolder',{...x,ParentID:parent}))}
function renameReqFolderModal(id){const f=reqFolder(id);showModal('重命名文件夹',`<label>文件夹名称<input name="Name" required value="${esc(f.Name)}"></label>`,x=>act('renameReqFolder',{...x,FolderID:id}))}
function reqDocModal(folderId){showModal('新增需求文档',`<label>标题<input name="Title" required></label>`,x=>act('createReqDoc',{...x,FolderID:folderId,Content:''}))}
function renameReqDocModal(doc){showModal('重命名需求文档',`<label>标题<input name="Title" required value="${esc(doc.Title)}"></label>`,x=>act('editReqDoc',{...x,DocID:doc.ID,Content:doc.Content}))}
// ---- AI 设计抽屉：对接 auto-test planner HTTP 服务 ----
// 假定同一时刻只有一个 AI 设计任务在跑（多任务并发不在此处理），
// 因此用全局变量记录"当前活跃任务属于哪个需求"即可驱动按钮态与抽屉重新打开。
const aiJobKey=doc=>`casehub-ai-job-${doc.ID}`;
// 设计完成后的结果摘要（导入条数 + 遗留问题）持久保存，关闭抽屉/刷新页面后仍展示，
// 直到用户点击"重新设计"才清除。
const aiResultKey=doc=>`casehub-ai-result-${doc.ID}`;
function loadAiResult(doc){try{return JSON.parse(localStorage.getItem(aiResultKey(doc)))}catch{return null}}
// 上一次提交过的表单：任务失败后点"重试"/"继续"不用重新填一遍，刷新页面也还在。
// 密码是被测系统的测试账号，不写进 localStorage，只留在内存里的 aiEstimates.values 中；
// 刷新后密码为空，表单里会提示需要重填。
const aiFormKey=doc=>`casehub-ai-form-${doc.ID}`;
function saveAiForm(doc,values,count,code){
  try{localStorage.setItem(aiFormKey(doc),JSON.stringify({values:{...values,testSecret:''},count,code}))}catch{}
}
function loadAiForm(doc){try{return JSON.parse(localStorage.getItem(aiFormKey(doc)))}catch{return null}}
let aiActiveDocId=null, aiSourceJobId=null, aiHandlingJobId=null;
function isAiActive(doc){return aiActiveDocId===doc.ID}
function setAiActive(doc){aiActiveDocId=doc.ID;syncAiButton()}
function clearAiActive(doc){if(aiActiveDocId===doc.ID)aiActiveDocId=null;syncAiButton()}
function syncAiButton(){
  const btn=$('#req-ai-design');
  if(!btn||reqFocus?.type!=='doc')return;
  const doc=state.reqDocs.find(x=>x.ID===reqFocus.id);
  if(!doc)return;
  btn.textContent=isAiActive(doc)?'AI 设计中':'AI 设计';
  btn.classList.toggle('ai-running',isAiActive(doc));
}
function closeAiStream(){aiSource?.close();aiSource=null;aiSourceJobId=null}
// 两个 auto-test 服务（planner / generator）共用：它们的错误体格式相同，CaseHub 的代理在
// 服务未配置或不可达时也返回同样的形状。
async function serviceRequest(path,options){
  const r=await fetch(path,options),ct=r.headers.get('content-type')||'';
  const x=ct.includes('application/json')?await r.json():null;
  if(!r.ok){const e=Error(x?.error?.message||'服务请求失败');e.status=r.status;e.code=x?.error?.code;throw e}
  return x;
}
function isAiDrawerOpen(doc){return aiDoc?.ID===doc.ID&&!$('#ai-drawer').classList.contains('hidden')}
function openAiDrawer(doc){
  aiDoc=doc;
  $('#ai-drawer-doc').textContent=`${doc.ID} · ${doc.Title}`;
  $('#ai-drawer').classList.remove('hidden');
  renderAiDrawer(doc);
}
$('#ai-drawer-close').onclick=()=>$('#ai-drawer').classList.add('hidden');

async function renderAiDrawer(doc){
  const body=$('#ai-drawer-body');
  body.innerHTML='<p class="meta">正在检查 AI 设计服务…</p>';
  if(aiPlannerEnabled===null){
    try{aiPlannerEnabled=(await serviceRequest('/api/planner/status')).enabled}
    catch{aiPlannerEnabled=false}
  }
  if(aiDoc?.ID!==doc.ID)return; // drawer moved to another doc while awaiting
  try{await loadAgentDefaults('playwright')}catch(e){body.innerHTML=`<p class="meta">读取 agent 默认配置失败：${esc(e.message)}</p><button type="button" id="ai-settings-retry">重试</button>`;$('#ai-settings-retry').onclick=()=>renderAiDrawer(doc);return}
  if(aiDoc?.ID!==doc.ID)return;
  await checkAiJob(doc);
}
async function checkAiJob(doc){
  const jobId=localStorage.getItem(aiJobKey(doc));
  if(!jobId){
    clearAiActive(doc);
    if(isAiDrawerOpen(doc)){const summary=loadAiResult(doc);summary?renderAiResult(doc,summary):renderAiForm(doc)}
    return;
  }
  try{
    const job=await serviceRequest(`/api/planner/jobs/${jobId}`);
    routeAiJob(doc,job);
  }catch(e){
    localStorage.removeItem(aiJobKey(doc));
    clearAiActive(doc);
    if(isAiDrawerOpen(doc))renderAiForm(doc,'读取任务状态失败，请重新开始。');
  }
}
// "建议覆盖用例数量"两步流程：开始分析 → 先调 /api/planner/estimate 评估并预填 → 用户可修改 →
// 确认后才提交完整 planner 任务（caseCount=该值，planner 输出 [caseCount-5, caseCount] 条用例）。
// 评估状态按需求保存在内存里（含已填表单），关闭抽屉再打开不会丢失评估中/评估结果。
// 同一步里还会建议"需求缩写"（用例编号 TC-<需求缩写>-<模块>-<类别>-NNN 的第二段），人工确认后保存到需求文档上，下次沿用。
const aiEstimates=new Map(); // docID -> {status:'draft'|'running'|'done', startedAt, count, code, rationale, notice, values, restored}
// 抽屉里表单内容的唯一来源。内存里没有（刷新过、或换了浏览器标签）就用上次提交的表单补上，
// 直接是"已评估"状态：数量和缩写都已经人工确认过，重试/继续不该再花一次评估。
function aiFormState(doc,ensure){
  let est=aiEstimates.get(doc.ID);
  if(est)return est;
  const saved=loadAiForm(doc);
  if(!saved&&!ensure)return undefined;
  est={status:'done',values:saved?.values||{},count:saved?.count||AI_DEFAULT_CASE_COUNT,
    code:saved?.code||liveReqDoc(doc).Code||'',rationale:'',restored:true};
  aiEstimates.set(doc.ID,est);
  return est;
}
const REQ_CODE_RE=/^[A-Z][A-Z0-9]{1,11}$/;
const liveReqDoc=doc=>state.reqDocs.find(x=>x.ID===doc.ID)||doc;
const AI_DEFAULT_CASE_COUNT=10, AI_MAX_CASE_COUNT=500, AI_CASE_COUNT_SLACK=5;
// 设计任务的时限由发起人决定：探索耗时取决于被测系统和用例数量，服务端的固定默认值（15 分钟）
// 对大需求经常不够。服务端接受 1 分钟–4 小时并自行封顶；这里沿用上次填的值（跨需求、跨刷新）。
const AI_TIMEOUT_KEY='casehub-ai-timeout-minutes';
const AI_DEFAULT_TIMEOUT_MIN=15, AI_MIN_TIMEOUT_MIN=1, AI_MAX_TIMEOUT_MIN=240;
function aiTimeoutMinutes(){
  const n=Number(localStorage.getItem(AI_TIMEOUT_KEY));
  return Number.isInteger(n)&&n>=AI_MIN_TIMEOUT_MIN&&n<=AI_MAX_TIMEOUT_MIN?n:AI_DEFAULT_TIMEOUT_MIN;
}
const AI_ESTIMATE_CLIENT_TIMEOUT_MS=135000; // 服务端评估上限 120 秒，额外留出代理/网络余量
const aiCaseRange=n=>`${Math.max(1,n-AI_CASE_COUNT_SLACK)}–${n}`;
function aiFormValues(form){const x=Object.fromEntries(new FormData(form));delete x.caseCount;delete x.reqCode;return x}
function aiRequirementPayload(doc,values,code){
  const d=liveReqDoc(doc);code=code||d.Code||'';
  return {requirements:[{id:d.ID,title:d.Title,content:d.Content||'',...(code?{code}:{})}],instructions:[values.instructions||'',reqRefContext(doc)].filter(Boolean).join('\n\n')};
}
function aiEstimateStatusText(est){
  const seconds=Math.floor((Date.now()-est.startedAt)/1000);
  return `正在评估这份需求至少需要多少条用例覆盖…${seconds?`（已等待 ${seconds} 秒，最长约 120 秒）`:''}`;
}
// 每种 agent 提供自己的参数表单；未来接入时在此注册独立的表单/提交实现。
const aiDesignAgents=[{id:'playwright',label:'aigc用例设计',render:renderPlaywrightAiForm,enabled:()=>aiPlannerEnabled,
  settingsFields:[
    {name:'baseUrl',label:'默认被测系统 URL',type:'url',placeholder:'https://test.example.com/login'},
    {name:'instructions',label:'默认补充说明',type:'textarea',placeholder:'默认覆盖范围、登录方式或探索约束'},
    {name:'testAccount',label:'默认用户名',type:'text'},
    {name:'testSecret',label:'默认密码',type:'text'},
    {name:'timeoutMinutes',label:'默认任务时长（分钟）',type:'number',min:AI_MIN_TIMEOUT_MIN,max:AI_MAX_TIMEOUT_MIN,required:true}
  ]}];
const agentDefaultsCache=new Map();
async function loadAgentDefaults(id){
  const config=await request(`/api/agent-settings/${encodeURIComponent(id)}`);
  agentDefaultsCache.set(id,config);return config;
}
function agentDefaults(agent){
  const saved=agentDefaultsCache.get(agent.id)||{};
  return Object.fromEntries(agent.settingsFields.map(field=>[field.name,saved[field.name]??(field.name==='timeoutMinutes'?AI_DEFAULT_TIMEOUT_MIN:'')]));
}
let agentSettingsDrafts=new Map(),agentRuntimeFiles=new Map(),agentSettingsGeneration=0;
function renderAgentSettings(agent){
  const values=agentSettingsDrafts.get(agent.id)||agentDefaults(agent),runtime=agentRuntimeFiles.get(agent.id);
  $('#settings-agents').innerHTML=aiDesignAgents.map(item=>`<button type="button" data-settings-agent="${esc(item.id)}" aria-current="${item.id===agent.id}">${esc(item.label)}</button>`).join('');
  $('#settings-content').innerHTML=`<h2>${esc(agent.label)}</h2><p class="meta">配置保存在服务端，所有设备共享。以下两部分分别保存。</p>
    <section class="settings-section"><h3>业务默认参数</h3><p class="meta">新设计任务选择此 agent 后自动带入，已有任务保留自己的参数。</p>
    <form id="agent-settings-form" autocomplete="off">${agent.settingsFields.map(field=>`<label>${esc(field.label)}${field.type==='textarea'?`<textarea name="${field.name}" placeholder="${esc(field.placeholder||'')}">${esc(values[field.name])}</textarea>`:`<input name="${field.name}" type="${field.type}" value="${esc(values[field.name])}" placeholder="${esc(field.placeholder||'')}"${field.required?' required':''}${field.type==='number'?` min="${field.min}" max="${field.max}" step="1"`:''} autocomplete="off">`}</label>`).join('')}
    <p class="settings-status" id="agent-settings-status" role="status"></p><div class="settings-actions"><button type="button" class="secondary" id="agent-settings-reset">恢复初始值</button><button type="submit">保存业务默认参数</button></div></form></section>
    <section class="settings-section settings-restart-required"><h3>Agent 配置 · setting.json <span class="settings-restart-mark" aria-label="修改后需重启">*</span></h3><p class="meta">完整预填文件内容，可修改、添加或删除任意参数。保存后直接写回文件；重启 agent 服务可确保全部配置生效。</p><p class="meta settings-path">${esc(runtime.path)}</p>${!runtime.exists?'<p class="meta" id="agent-runtime-missing">文件尚不存在，保存时创建。</p>':''}
    <form id="agent-runtime-form"><label>完整 JSON 配置 <span class="settings-restart-mark" aria-hidden="true">*</span><textarea aria-describedby="agent-runtime-restart-hint" id="agent-runtime-json" name="content" spellcheck="false" rows="16">${esc(runtime.draft??runtime.content)}</textarea></label><p class="settings-restart-hint" id="agent-runtime-restart-hint"><span aria-hidden="true">*</span> 修改后请在后台重启 agent 服务，以确保全部配置生效；在 AI 设计中重新选择 agent 不会重启服务。</p><p class="settings-status" id="agent-runtime-status" role="status"></p><div class="settings-actions"><button type="button" class="secondary" id="agent-runtime-reload">重新读取文件</button><button type="submit">保存 setting.json</button></div></form></section>`;
  $$('#settings-agents [data-settings-agent]').forEach(button=>button.onclick=()=>renderAgentSettings(aiDesignAgents.find(item=>item.id===button.dataset.settingsAgent)));
  const form=$('#agent-settings-form');
  form.oninput=()=>{agentSettingsDrafts.set(agent.id,Object.fromEntries(new FormData(form)));$('#agent-settings-status').textContent='有未保存的修改'};
  $('#agent-settings-reset').onclick=()=>{
    agentSettingsDrafts.set(agent.id,Object.fromEntries(agent.settingsFields.map(field=>[field.name,field.name==='timeoutMinutes'?AI_DEFAULT_TIMEOUT_MIN:''])));
    renderAgentSettings(agent);$('#agent-settings-status').textContent='已恢复初始值，点击保存后生效';
  };
  form.onsubmit=async e=>{
    e.preventDefault();if(!form.reportValidity())return;
    const values=Object.fromEntries(new FormData(form));values.timeoutMinutes=Number(values.timeoutMinutes);
    const button=form.querySelector('[type="submit"]'),status=form.querySelector('[role="status"]');button.disabled=true;
    const generation=agentSettingsGeneration;
    try{
      const config=await request(`/api/agent-settings/${encodeURIComponent(agent.id)}`,{method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify(values)});
      agentDefaultsCache.set(agent.id,config);
      if(generation===agentSettingsGeneration)status.textContent='业务默认参数已保存';
      toast('业务默认参数已保存');
    }catch(error){status.textContent=`保存失败：${error.message}`}finally{button.disabled=false}
  };
  const runtimeForm=$('#agent-runtime-form'),editor=$('#agent-runtime-json'),status=$('#agent-runtime-status');
  editor.oninput=()=>{runtime.draft=editor.value;status.textContent='有未保存的修改'};
  $('#agent-runtime-reload').onclick=async()=>{
    if(runtime.draft!==undefined&&runtime.draft!==runtime.content&&!confirm('重新读取会放弃未保存的 JSON 修改，是否继续？'))return;
    const generation=agentSettingsGeneration;
    try{const latest=await request(`/api/agent-settings/${agent.id}/runtime`);if(generation!==agentSettingsGeneration)return;agentRuntimeFiles.set(agent.id,latest);renderAgentSettings(agent)}
    catch(error){status.textContent=`读取失败：${error.message}`}
  };
  runtimeForm.onsubmit=async e=>{
    e.preventDefault();
    try{const value=JSON.parse(editor.value);if(!value||Array.isArray(value)||typeof value!=='object')throw Error('配置必须是 JSON 对象')}
    catch{status.textContent='请填写有效的 JSON 对象';return}
    const button=runtimeForm.querySelector('[type="submit"]');button.disabled=true;
    const content=editor.value,generation=agentSettingsGeneration;
    try{
      const saved=await request(`/api/agent-settings/${agent.id}/runtime`,{method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify({content,revision:runtime.revision})});
      if(generation!==agentSettingsGeneration)return;
      runtime.content=saved.content;runtime.revision=saved.revision;runtime.exists=true;$('#agent-runtime-missing')?.remove();
      status.textContent='setting.json 已保存；重启 agent 服务可确保全部配置生效';
    }catch(error){status.textContent=`保存失败：${error.message}`}finally{button.disabled=false}
  };
}
$('#settings-open').onclick=async()=>{
  const generation=++agentSettingsGeneration;agentSettingsDrafts=new Map();agentRuntimeFiles=new Map();
  $('#settings-agents').replaceChildren();$('#settings-content').innerHTML='<p class="meta">正在读取 agent 配置…</p>';$('#settings-dialog').showModal();
  try{
    await Promise.all(aiDesignAgents.map(async agent=>{const [,runtime]=await Promise.all([loadAgentDefaults(agent.id),request(`/api/agent-settings/${agent.id}/runtime`)]);if(generation===agentSettingsGeneration)agentRuntimeFiles.set(agent.id,runtime)}));
    if(generation===agentSettingsGeneration&&$('#settings-dialog').open)renderAgentSettings(aiDesignAgents[0]);
  }catch(error){if(generation!==agentSettingsGeneration)return;$('#settings-content').innerHTML=`<p class="meta">读取配置失败：${esc(error.message)}</p><button type="button" id="settings-retry">重试</button>`;$('#settings-retry').onclick=()=>{$('#settings-dialog').close();$('#settings-open').click()}}
};
$('#settings-close').onclick=()=>$('#settings-dialog').close();
$('#settings-dialog').onclose=()=>{agentSettingsGeneration++;agentSettingsDrafts.clear();agentRuntimeFiles.clear()};
const aiSelectedAgents=new Map();
function renderAiForm(doc,notice,continueFrom){
  const est=aiFormState(doc,Boolean(continueFrom));
  // 旧版表单没有 agentId，均属于 Playwright。
  const selected=continueFrom?(est?.values?.agentId||'playwright')
    :(aiSelectedAgents.get(doc.ID)??(est?(est.values.agentId||'playwright'):''));
  const locked=Boolean(continueFrom)||est?.status==='running';
  $('#ai-drawer-body').innerHTML=`<label>设计 Agent<select id="ai-agent"${locked?' disabled':''}><option value="">请选择 agent</option>${aiDesignAgents.map(agent=>`<option value="${esc(agent.id)}"${selected===agent.id?' selected':''}>${esc(agent.label)}</option>`).join('')}</select></label><div id="ai-agent-parameters"></div>`;
  $('#ai-agent').onchange=e=>{aiSelectedAgents.set(doc.ID,e.target.value);renderAiForm(doc,notice)};
  const agent=aiDesignAgents.find(agent=>agent.id===selected);
  if(!agent){
    $('#ai-agent-parameters').innerHTML='<p class="meta">请先选择 agent，再填写该 agent 所需的参数。</p>';
    return;
  }
  if(!agent.enabled()){
    $('#ai-agent-parameters').innerHTML=`<p class="meta">${esc(agent.label)} 服务未配置，暂时无法使用。</p>`;
    return;
  }
  agent.render(doc,notice,continueFrom);
}
// continueFrom：从失败任务的"继续"进来，提交时带上该任务 ID，服务端接着那次探索继续，不重新探索。
function renderPlaywrightAiForm(doc,notice,continueFrom){
  // 用例字段特意不叫 name="username"/"password"，且密码框用 type="text" +
  // -webkit-text-security 伪装遮罩：这些只是被测系统的测试账号，不是本机
  // 登录凭据，但字段名/类型撞上 Chrome 的登录表单识别规则后，提交时会触发
  // 它的"密码遭遇数据泄露"弹窗（表单没有真正提交/跳转也会触发，JS 端
  // preventDefault 拦不住）。换掉字段名和输入类型可以让 Chrome 从一开始就不
  // 把这当成登录密码框，从根上避免弹窗，同时视觉上仍然是圆点遮罩。
  const est=aiFormState(doc,Boolean(continueFrom)),v=est?.values||agentDefaults(aiDesignAgents.find(agent=>agent.id==='playwright')),done=est?.status==='done',running=est?.status==='running';
  const countField=done
    ?`<input name="caseCount" type="number" min="1" max="${AI_MAX_CASE_COUNT}" step="1" required value="${est.count}">`
    :`<input name="caseCount" type="number" disabled placeholder="点击“开始分析”后由 AI 评估">`;
  const savedCode=liveReqDoc(doc).Code||'';
  const codeField=done
    ?`<input name="reqCode" required maxlength="12" pattern="[A-Z][A-Z0-9]{1,11}" autocomplete="off" spellcheck="false" placeholder="如 LOGIN" value="${esc(est.code||'')}">`
    :`<input name="reqCode" disabled placeholder="${savedCode?esc(savedCode):'点击“开始分析”后由 AI 建议'}">`;
  const estimateInfo=running?`<p class="meta" id="ai-estimate-status">${aiEstimateStatusText(est)}</p>`
    :done?`${est.notice?`<p class="meta">${esc(est.notice)}</p>`:''}${est.rationale?`<p class="meta ai-estimate-rationale">${esc(est.rationale)}</p>`:''}<p class="meta" id="ai-case-range">AI 将输出 ${aiCaseRange(est.count)} 条用例</p>`:'';
  const actions=done?`${continueFrom?'':'<button type="button" class="secondary" id="ai-reestimate">重新评估</button>'}<button type="submit">${continueFrom?'继续设计':'确认并开始设计'}</button>`
    :`<button type="submit"${running?' disabled':''}>${running?'评估中…':'开始分析'}</button>`;
  const continueCard=continueFrom?`<div class="card"><h3>继续上次中断的设计</h3><p class="meta">接着上次的探索往下做，不会从头再探索一遍；浏览器会重新打开被测系统。可以先调大下面的任务超时时间再继续。</p></div>`:'';
  const restored=est?.restored&&!v.testSecret&&(v.baseUrl||v.testAccount)?'<p class="meta">已恢复上次提交的表单；测试账号密码没有保存在浏览器里，被测系统需要登录时请重新填写。</p>':'';
  $('#ai-agent-parameters').innerHTML=`${notice?`<p class="meta">${esc(notice)}</p>`:''}${continueCard}${restored}<form id="ai-form" autocomplete="off"><input type="hidden" name="agentId" value="playwright"><label>被测系统 URL<input name="baseUrl" required placeholder="https://test.example.com/login" autocomplete="off" value="${esc(v.baseUrl||'')}"></label><label>补充说明（可选）<textarea name="instructions" placeholder="覆盖范围、登录方式；也可以限制探索行为，例如：提交后确认任务已开始即可，不要等待任务运行完成">${esc(v.instructions||'')}</textarea><small class="meta">这里写的内容是硬性约束：AI 探索时会照做，被它挡住的验证会作为"未验证范围"返回，而不是绕开约束去试。</small></label><label>测试账号 · 用户名（可选）<input name="testAccount" autocomplete="off" value="${esc(v.testAccount||'')}"></label><label>测试账号 · 密码（可选）<input name="testSecret" type="text" class="fake-password" autocomplete="off" spellcheck="false" value="${esc(v.testSecret||'')}"></label><label>建议覆盖用例数量${countField}</label><label>需求缩写（用例编号 TC-<b id="ai-code-preview">${esc((done&&est.code)||savedCode||'XXX')}</b>-模块-类别-001）${codeField}</label><label>任务超时时间（分钟）<input name="timeoutMinutes" type="number" min="${AI_MIN_TIMEOUT_MIN}" max="${AI_MAX_TIMEOUT_MIN}" step="1" required value="${esc(v.timeoutMinutes??aiTimeoutMinutes())}"><small class="meta">探索时间取决于被测系统和用例数量；超时任务会以 JOB_TIMEOUT 失败，草稿不会保留，但已探索的内容留在服务端，可以点「继续」接着跑。</small></label>${estimateInfo}<p class="drawer-actions">${actions}</p></form>`;
  const form=$('#ai-form');
  // 评估期间/评估后继续编辑的表单内容同步进状态，重新渲染（关闭再打开抽屉）时保留。
  form.oninput=()=>{
    let cur=aiEstimates.get(doc.ID);
    if(!cur){cur={status:'draft',values:{}};aiEstimates.set(doc.ID,cur)}
    cur.values=aiFormValues(form);
    const n=Number(form.elements.caseCount.value),range=$('#ai-case-range');
    if(range)range.textContent=Number.isInteger(n)&&n>=1&&n<=AI_MAX_CASE_COUNT?`AI 将输出 ${aiCaseRange(n)} 条用例`:`请输入 1–${AI_MAX_CASE_COUNT} 的整数`;
    if(cur&&cur.status==='done'&&Number.isInteger(n))cur.count=n;
    const codeInput=form.elements.reqCode;
    if(cur&&cur.status==='done'){
      const up=codeInput.value.toUpperCase().replace(/[^A-Z0-9]/g,'');
      if(up!==codeInput.value)codeInput.value=up;
      cur.code=up;$('#ai-code-preview').textContent=up||'XXX';
    }
  };
  if(done&&!continueFrom)$('#ai-reestimate').onclick=()=>runAiEstimate(doc,aiFormValues(form));
  form.onsubmit=async e=>{
    e.preventDefault();
    const values=aiFormValues(form);
    if(!done)return runAiEstimate(doc,values);
    const caseCount=Number(form.elements.caseCount.value);
    if(!Number.isInteger(caseCount)||caseCount<1||caseCount>AI_MAX_CASE_COUNT){toast(`建议覆盖用例数量需为 1–${AI_MAX_CASE_COUNT} 的整数`,true);return}
    const code=form.elements.reqCode.value.trim();
    if(!REQ_CODE_RE.test(code)){toast('需求缩写需为 2–12 位大写英文字母或数字，以字母开头，不含 -',true);return}
    const timeoutMinutes=Number(form.elements.timeoutMinutes.value);
    if(!Number.isInteger(timeoutMinutes)||timeoutMinutes<AI_MIN_TIMEOUT_MIN||timeoutMinutes>AI_MAX_TIMEOUT_MIN){toast(`任务超时时间需为 ${AI_MIN_TIMEOUT_MIN}–${AI_MAX_TIMEOUT_MIN} 分钟的整数`,true);return}
    try{localStorage.setItem(AI_TIMEOUT_KEY,String(timeoutMinutes))}catch{} // 下次设计沿用这次的时限
    const btn=form.querySelector('button[type="submit"]');btn.disabled=true;
    if(code!==(liveReqDoc(doc).Code||'')){
      try{await act('setReqDocCode',{DocID:doc.ID,Code:code})}catch{btn.disabled=false;return} // act 已提示原因（如缩写重复）
      // 只更新详情页上的缩写标签：整页 renderReqFocus 会重建编辑器，丢掉未保存的需求正文修改。
      if(reqFocus?.type==='doc'&&reqFocus.id===doc.ID&&$('#req-doc-code'))$('#req-doc-code').textContent=code;
    }
    const {requirements,instructions}=aiRequirementPayload(doc,values,code);
    const payload={requirements,target:{baseUrl:values.baseUrl},context:{instructions},caseCount,timeoutMs:timeoutMinutes*60000,
      ...(continueFrom?{continueFrom}:{})};
    if(values.testAccount||values.testSecret)payload.context.testData={username:values.testAccount||'',password:values.testSecret||''};
    try{
      const job=await serviceRequest('/api/planner/jobs',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(payload)});
      // 提交过的表单留着（新对象，作废仍在跑的评估），失败后重试/继续可以直接用。
      aiEstimates.set(doc.ID,{status:'done',count:caseCount,code,rationale:est?.rationale||'',values});
      saveAiForm(doc,values,caseCount,code);
      localStorage.setItem(aiJobKey(doc),job.id);
      routeAiJob(doc,job);
    }catch(err){
      toast(err.message,true);btn.disabled=false;
      // 会话已清理、或这次中断已经被继续过：回到普通表单，别让人反复点一个已经失效的"继续"。
      if(continueFrom&&err.status===409){localStorage.removeItem(aiJobKey(doc));renderAiForm(doc,'上次的任务已经无法继续，请重新开始设计。')}
    }
  };
}
async function runAiEstimate(doc,values){
  if(aiEstimates.get(doc.ID)?.status==='running')return;
  const est={status:'running',startedAt:Date.now(),values};
  aiEstimates.set(doc.ID,est);
  if(isAiDrawerOpen(doc))renderAiForm(doc);
  const ticker=setInterval(()=>{const el=$('#ai-estimate-status');if(el&&isAiDrawerOpen(doc)&&aiEstimates.get(doc.ID)===est)el.textContent=aiEstimateStatusText(est)},1000);
  const controller=new AbortController(),abortTimer=setTimeout(()=>controller.abort(),AI_ESTIMATE_CLIENT_TIMEOUT_MS);
  let next;
  try{
    const {requirements,instructions}=aiRequirementPayload(doc,values);
    const r=await serviceRequest('/api/planner/estimate',{method:'POST',headers:{'Content-Type':'application/json'},signal:controller.signal,
      body:JSON.stringify({requirements,context:{instructions}})});
    next={count:r.suggestedCaseCount,code:r.requirementCodes?.find(x=>x.requirement===doc.ID)?.code||liveReqDoc(doc).Code||'',rationale:r.rationale||''};
  }catch(e){
    const reason=controller.signal.aborted?'评估超时':`评估失败：${e.message}`;
    toast(reason,true);
    next={count:AI_DEFAULT_CASE_COUNT,code:liveReqDoc(doc).Code||'',rationale:'',notice:`${reason}，已填入默认值 ${AI_DEFAULT_CASE_COUNT}，可修改后确认。`};
  }finally{clearTimeout(abortTimer);clearInterval(ticker)}
  if(aiEstimates.get(doc.ID)!==est)return; // superseded (e.g. a job was started elsewhere)
  aiEstimates.set(doc.ID,{status:'done',...next,values:est.values});
  if(isAiDrawerOpen(doc)&&!isAiActive(doc)&&!localStorage.getItem(aiJobKey(doc)))renderAiForm(doc);
}
function aiStageLabel(job){
  const status={queued:'排队中',running:'进行中',succeeded:'已完成',failed:'失败',cancelled:'已取消'}[job.status]||job.status;
  return job.stage?`${status} · ${esc(job.stage)}`:status;
}
// 任务状态的唯一分发点：无论抽屉是否打开（可关闭，关闭后任务继续在后台跑），
// 都会在这里更新"AI 设计中"按钮态、维持 SSE 订阅，并在完成时自动导入。
function routeAiJob(doc,job){
  const terminal=['succeeded','failed','cancelled'].includes(job.status);
  if(terminal&&aiSourceJobId===job.id)closeAiStream();
  if(!terminal){
    setAiActive(doc);
    if(isAiDrawerOpen(doc))renderAiRunning(doc,job);
    ensureAiStream(doc,job.id);
    return;
  }
  clearAiActive(doc);
  if(job.status==='succeeded')return handleAiSuccess(doc,job);
  // 失败的任务留在 localStorage 里：刷新或重开抽屉后仍能看到失败原因，并选择重试还是继续
  // （continuable 的会话在服务端还留着）。取消的任务没什么可继续的，直接清掉。
  if(job.status!=='failed')localStorage.removeItem(aiJobKey(doc));
  if(isAiDrawerOpen(doc))renderAiTerminal(doc,job);
}
function renderAiRunning(doc,job){
  const limit=job.timeoutMs?`<span>时限 <b>${Math.round(job.timeoutMs/60000)} 分钟</b></span>`:'';
  $('#ai-drawer-body').innerHTML=`<div class="card meta-grid"><span>状态 <b>${aiStageLabel(job)}</b></span>${limit}</div><div id="ai-log" class="ai-log"></div><p class="drawer-actions"><button type="button" class="secondary" id="ai-cancel">取消任务</button></p>`;
  $('#ai-cancel').onclick=async()=>{
    try{await serviceRequest(`/api/planner/jobs/${job.id}`,{method:'DELETE'})}catch(e){toast(e.message,true)}
  };
}
function aiLogLine(msg){
  const log=$('#ai-log');
  if(!log)return;
  const atBottom=log.scrollTop+log.clientHeight>=log.scrollHeight-4;
  const line=document.createElement('div');
  line.textContent=msg;
  log.appendChild(line);
  while(log.childElementCount>200)log.removeChild(log.firstChild);
  if(atBottom)log.scrollTop=log.scrollHeight;
}
function ensureAiStream(doc,jobId){
  if(aiSource&&aiSourceJobId===jobId)return; // already subscribed, keep it (drawer may be closed)
  closeAiStream();
  const source=new EventSource(`/api/planner/jobs/${jobId}/events`);
  aiSource=source;aiSourceJobId=jobId;
  source.addEventListener('snapshot',e=>{
    const job=JSON.parse(e.data);
    if(isAiDrawerOpen(doc)){
      const badge=$('#ai-drawer-body .meta-grid b');
      if(badge)badge.textContent=aiStageLabel(job);
    }
    if(['succeeded','failed','cancelled'].includes(job.status))routeAiJob(doc,job);
  });
  source.addEventListener('progress',e=>{
    const ev=JSON.parse(e.data);
    if(isAiDrawerOpen(doc)){
      const badge=$('#ai-drawer-body .meta-grid b');
      if(badge)badge.textContent=aiStageLabel(ev);
      aiLogLine(`[${fmt(ev.createdAt)}] ${ev.stage||''} ${ev.message||''}${ev.tool?` (${ev.tool} ${ev.toolStatus||''})`:''}`.trim());
    }
    if(['succeeded','failed','cancelled'].includes(ev.status))serviceRequest(`/api/planner/jobs/${jobId}`).then(job=>routeAiJob(doc,job));
  });
  source.addEventListener('reset',e=>{
    if(!isAiDrawerOpen(doc))return;
    const r=JSON.parse(e.data);
    aiLogLine(`……${r.message||'更早的进度记录已丢失'}`);
  });
}
// 失败后有两条路：重试（同样的表单从头开始）和继续（服务端还留着这次的探索会话，接着往下做）。
// 超时失败最值得"继续"——探索已经花掉的时间不用再花一遍。
function renderAiTerminal(doc,job){
  const cancelled=job.status==='cancelled';
  const hint=job.continuable?'<p class="meta">中断前的探索还留在服务端：「继续」接着往下做（可以先调大超时时间），「重试」用同样的表单从头开始。</p>':'';
  $('#ai-drawer-body').innerHTML=`<div class="card meta-grid"><span>状态 <b>${aiStageLabel(job)}</b></span></div>${job.error?`<p class="meta">${esc(job.error.code)}：${esc(job.error.message)}</p>`:cancelled?'<p class="meta">任务已取消。</p>':''}${hint}<p class="drawer-actions"><button type="button"${job.continuable?' class="secondary"':''} id="ai-retry">重试</button>${job.continuable?'<button type="button" id="ai-continue">继续</button>':''}</p>`;
  $('#ai-retry').onclick=()=>{localStorage.removeItem(aiJobKey(doc));renderAiForm(doc)};
  if(job.continuable)$('#ai-continue').onclick=()=>renderAiForm(doc,'',job.id);
}
// 设计完成后自动创建"用例评审"目录并导入草稿用例，无需人工点击导入。
async function handleAiSuccess(doc,job){
  if(aiHandlingJobId===job.id)return;
  aiHandlingJobId=job.id;
  if(isAiDrawerOpen(doc))$('#ai-drawer-body').innerHTML='<p class="meta">设计已完成，正在自动导入到"用例评审"…</p>';
  try{
    const result=await serviceRequest(`/api/planner/jobs/${job.id}/result`);
    await importAiResult(doc,result);
    const summary={count:result.cases.length,limitations:result.limitations||[],issues:result.issues||[]};
    localStorage.setItem(aiResultKey(doc),JSON.stringify(summary));
    localStorage.removeItem(aiJobKey(doc));
    toast(`AI 设计已完成，已自动导入 ${result.cases.length} 条用例到"用例评审"`);
    if(isAiDrawerOpen(doc))renderAiResult(doc,summary);
  }catch(e){
    toast(`AI 设计结果自动导入失败：${e.message}`,true);
    if(isAiDrawerOpen(doc)){
      $('#ai-drawer-body').innerHTML=`<p class="meta">自动导入失败：${esc(e.message)}</p><p class="drawer-actions"><button type="button" id="ai-retry-import">重试导入</button></p>`;
      $('#ai-retry-import').onclick=()=>{aiHandlingJobId=null;handleAiSuccess(doc,job).catch(()=>{})};
    }
  }finally{
    if(aiHandlingJobId===job.id)aiHandlingJobId=null;
  }
}
// planner 给出的未验证/受限范围是 {risk, summary}、探索中发现的问题是 {risk, scenario, symptom}，
// 两者都已按风险高→低排序；更早保存的结果里 limitations 是纯文本，照原样显示在最后。
const AI_RISKS={high:'高',medium:'中',low:'低'};
function aiRiskListHTML(list,textHTML){
  const items=(list||[]).map((l,i)=>typeof l==='string'?{risk:'',html:esc(l),i}:{risk:l.risk,html:textHTML(l),i});
  const rank=r=>r in AI_RISKS?Object.keys(AI_RISKS).indexOf(r):3;
  items.sort((a,b)=>rank(a.risk)-rank(b.risk)||a.i-b.i);
  return `<ul class="ai-risk-list">${items.map(l=>`<li>${l.risk in AI_RISKS?`<span class="risk-tag risk-${l.risk}">${AI_RISKS[l.risk]}</span>`:''}<span>${l.html}</span></li>`).join('')}</ul>`;
}
// 发现的问题排在前面：那是探索时实测到的缺陷，用例仍按需求的正确行为编写，需要人来决定怎么处理。
function renderAiResult(doc,summary){
  const issues=summary.issues?.length?`<div class="card"><h3>探索中发现的问题 <small class="meta">按风险从高到低</small></h3>${aiRiskListHTML(summary.issues,i=>`<b>${esc(i.scenario)}</b>：${esc(i.symptom)}`)}</div>`:'';
  const limitations=summary.limitations?.length?`<div class="card"><h3>未验证/受限范围 <small class="meta">按风险从高到低</small></h3>${aiRiskListHTML(summary.limitations,l=>esc(l.summary))}</div>`:'';
  $('#ai-drawer-body').innerHTML=`<div class="card meta-grid"><span>已导入用例 <b>${summary.count}</b></span></div>${issues}${limitations}<p class="meta">已自动创建目录并导入到"用例评审"，请前往评审。</p><p class="drawer-actions"><button type="button" class="secondary" id="ai-restart">重新设计</button></p>`;
  $('#ai-restart').onclick=()=>{localStorage.removeItem(aiResultKey(doc));renderAiForm(doc)};
}
// 按用例编号 TC-<REQ>-<MOD>-<CAT>-NNN 归档：用例评审下建 需求 / 功能模块 / 测试类别 三层文件夹。
// 文件夹名用中文（需求标题、planner 给出的模块中文名、"功能测试"等），编号前缀存在文件夹的 Code 上
// （REQ / REQ-MOD / REQ-MOD-CAT），同 Code 复用；用例建在最里层，由 CaseHub 按 Code 续编号
// （planner 结果里的序号只在单次结果内递增，可能与已有用例重复）。编号不符合该格式的旧结果放在需求文件夹下（随机编号）。
const AI_CASE_ID_RE=/^TC-([A-Z][A-Z0-9]{1,11})-([A-Z][A-Z0-9]{1,11})-(FUNC|REL|PERF|SEC|COMPAT|UX)-\d{3,}$/;
const TEST_CATEGORY_NAMES={FUNC:'功能测试',REL:'可靠性测试',PERF:'性能测试',SEC:'安全测试',COMPAT:'兼容性测试',UX:'易用性测试'};
async function ensurePendingFolder(parentID,code,name){
  // 早期按编号命名、没有 Code 的文件夹也认作同一个
  const found=state.pendingFolders.find(f=>f.ParentID===parentID&&(code?(f.Code===code||(!f.Code&&f.Name===code)):f.Name===name));
  if(found)return found.ID;
  const before=new Set(state.pendingFolders.map(f=>f.ID));
  const out=await act('createPendingFolder',{ParentID:parentID,Name:name,Code:code});
  return out.state.pendingFolders.find(f=>!before.has(f.ID)).ID;
}
async function importAiResult(doc,result){
  const d=liveReqDoc(doc);
  for(const c of result.cases){
    const m=AI_CASE_ID_RE.exec(c.case_id||'');
    let folderID;
    if(m){
      const [,req,mod,cat]=m;
      const moduleName=(result.modules||[]).find(x=>x.requirement===c.request&&x.code===mod)?.name||mod;
      folderID=await ensurePendingFolder('pending-root',req,d.Code===req?d.Title:req);
      folderID=await ensurePendingFolder(folderID,`${req}-${mod}`,moduleName);
      folderID=await ensurePendingFolder(folderID,`${req}-${mod}-${cat}`,TEST_CATEGORY_NAMES[cat]);
    }else{
      folderID=await ensurePendingFolder('pending-root',d.Code||'',d.Title);
    }
    await act('createPendingCase',{FolderID:folderID,Title:c.name,Priority:c.priority,Preconditions:c.precondition,Steps:c.steps,Expected:c.expects});
  }
}

// ---- Pending case review (用例评审) ----
function actReview(type,data={}){return act(type,data).then(()=>renderReqFocus())}
const pendingFolder=id=>state.pendingFolders.find(f=>f.ID===id);
const pendingChildren=id=>state.pendingFolders.filter(f=>f.ParentID===id);
const pendingCasesIn=id=>state.pendingCases.filter(c=>c.FolderID===id);
function pendingRoots(){return state.pendingFolders.filter(f=>!f.ParentID||!state.pendingFolders.some(x=>x.ID===f.ParentID))}
function pendingDescendantFolders(f){let out=[];function walk(id){pendingChildren(id).forEach(x=>{out.push(x.ID);walk(x.ID)})}walk(f.ID);return out}
function reviewBadge(c){return c.Review==='passed'?'<span class="result passed" title="评审通过"></span>':c.Review==='rejected'?'<span class="result failed" title="评审不通过"></span>':''}
// 用例评审树的多选（批量删除）：勾选用例或文件夹（=其下全部用例），顶部批量栏显示已选数量。
const reviewSelected=new Set(), reviewPickedFolders=new Set(); // 勾选过的文件夹：批量导入时用来确定保留的最上层目录
function reviewCaseRow(c,depth){return `<div class="tree-row case-row" style="padding-left:${8+depth*17}px" data-review-case="${c.ID}" title="${esc(caseLabel(c))}"><input class="review-case-check" type="checkbox" ${reviewSelected.has(c.ID)?'checked':''}><span class="label">${esc(caseLabel(c))}</span>${reviewBadge(c)}</div>`}
function reviewTreeHTML(){
  function node(f,depth){
    let children=pendingChildren(f.ID),own=pendingCasesIn(f.ID);
    return `<div class="tree-row folder-row" style="padding-left:${8+depth*17}px" data-review-folder="${f.ID}"><input class="review-folder-check" type="checkbox"><span class="chev">▾</span><span>📁</span><span class="label">${esc(f.Name)}</span></div><div>${own.map(c=>reviewCaseRow(c,depth+1)).join('')}${children.map(ch=>node(ch,depth+1)).join('')}</div>`;
  }
  return pendingRoots().map(r=>node(r,0)).join('')||'<p class="meta">暂无待评审用例</p>';
}
function renderReviewTree(){
  const alive=new Set(state.pendingCases.map(c=>c.ID));
  for(const id of [...reviewSelected])if(!alive.has(id))reviewSelected.delete(id); // 已被删除/导入的不再算已选
  for(const id of [...reviewPickedFolders])if(!pendingFolder(id))reviewPickedFolders.delete(id);
  const box=$('#review-tree');box.innerHTML=reviewTreeHTML();bindReviewTree(box);updateReviewBulk();
}
function updateReviewBulk(){
  const n=reviewSelected.size;
  $('#review-bulk').classList.toggle('hidden',!n);
  $('#review-selected-count').textContent=`已选 ${n} 条用例`;
  $$('#review-tree [data-review-folder]').forEach(e=>{
    const fc=e.querySelector('.review-folder-check'),ids=pendingCaseIdsIn(e.dataset.reviewFolder),sel=ids.filter(id=>reviewSelected.has(id)).length;
    fc.checked=ids.length>0&&sel===ids.length;fc.indeterminate=sel>0&&sel<ids.length;fc.disabled=!ids.length;
  });
  $$('#review-tree .review-case-check').forEach(cb=>cb.checked=reviewSelected.has(cb.closest('[data-review-case]').dataset.reviewCase));
}
$('#review-bulk [data-review-bulk="clear"]').onclick=()=>{reviewSelected.clear();reviewPickedFolders.clear();updateReviewBulk()};
// 批量导入的来源层：优先取勾选过、且恰好覆盖全部已选用例的最上层文件夹（保留该文件夹本身）；
// 否则取已选用例所在文件夹的最近公共祖先（保留该层）；只选了一条用例时直接放进父目录。
function reviewImportSource(ids){
  const sel=new Set(ids);
  const covering=[...reviewPickedFolders].filter(fid=>{const inside=pendingCaseIdsIn(fid);return inside.length&&inside.every(id=>sel.has(id))&&ids.every(id=>inside.includes(id))});
  const ancestors=fid=>{const out=[];for(let id=fid,g=0;id&&g<200;g++){out.unshift(id);if(id==='pending-root')break;id=pendingFolder(id)?.ParentID||'pending-root'}return out};
  if(covering.length){const top=covering.map(ancestors).sort((a,b)=>a.length-b.length)[0];return pendingParentOf(top[top.length-1])}
  const folderOf=id=>state.pendingCases.find(c=>c.ID===id)?.FolderID||'pending-root';
  if(ids.length===1)return folderOf(ids[0]);
  const paths=ids.map(id=>ancestors(folderOf(id)));
  let lca='pending-root';
  for(let i=0;i<paths[0].length&&paths.every(p=>p[i]===paths[0][i]);i++)lca=paths[0][i];
  return pendingParentOf(lca);
}
$('#review-bulk [data-review-bulk="import"]').onclick=()=>{const ids=[...reviewSelected];if(ids.length)importReviewModal(ids,reviewImportSource(ids))};
$('#review-bulk [data-review-bulk="delete"]').onclick=async()=>{
  const ids=[...reviewSelected];
  if(!ids.length||!confirm(`确定删除选中的 ${ids.length} 条待评审用例？删除后无法恢复。`))return;
  try{await actReview('deletePendingCases',{CaseIDs:ids})}catch{return}
  ids.forEach(id=>reviewSelected.delete(id));updateReviewBulk();
  toast(`已删除 ${ids.length} 条待评审用例`);
};
function bindReviewTree(root){
  root.querySelectorAll('[data-review-folder]').forEach(e=>{let key=e.dataset.reviewFolder;if(closedReviewFolders.has(key))e.classList.add('closed');e.querySelector('.chev').onclick=x=>{x.stopPropagation();let closed=e.classList.toggle('closed');closed?closedReviewFolders.add(key):closedReviewFolders.delete(key)};const fc=e.querySelector('.review-folder-check');fc.onclick=x=>{x.stopPropagation();const fid=e.dataset.reviewFolder;fc.checked?reviewPickedFolders.add(fid):reviewPickedFolders.delete(fid);pendingCaseIdsIn(fid).forEach(id=>fc.checked?reviewSelected.add(id):reviewSelected.delete(id));updateReviewBulk()};e.onclick=()=>setReqFocus({type:'reviewFolder',id:e.dataset.reviewFolder});e.oncontextmenu=x=>menu(x,reviewFolderMenu(e.dataset.reviewFolder))});
  root.querySelectorAll('[data-review-case]').forEach(e=>{const cb=e.querySelector('.review-case-check');cb.onclick=x=>{x.stopPropagation();cb.checked?reviewSelected.add(e.dataset.reviewCase):reviewSelected.delete(e.dataset.reviewCase);updateReviewBulk()};e.onclick=()=>setReqFocus({type:'reviewCase',id:e.dataset.reviewCase});e.oncontextmenu=x=>menu(x,reviewCaseMenu(e.dataset.reviewCase))});
}
function pendingCaseIdsIn(folderId){
  const f=pendingFolder(folderId);
  const scope=f?[f.ID,...pendingDescendantFolders(f)]:[folderId];
  return state.pendingCases.filter(c=>scope.includes(c.FolderID)).map(c=>c.ID);
}
function reviewFolderMenu(id){const items=[['查看详情',()=>setReqFocus({type:'reviewFolder',id})],['新建文件夹',()=>pendingFolderModal(id)],['新增用例',()=>pendingCaseModal(null,id)],['导入到版本…',()=>importReviewModal(pendingCaseIdsIn(id),pendingParentOf(id))],['重命名空文件夹',()=>renamePendingFolderModal(id)]];if(id!=='pending-root')items.push(['移动到…',()=>movePendingFolderModal(id)],['删除空文件夹',()=>{if(confirm('确定删除这个空文件夹？'))actReview('deletePendingFolder',{FolderID:id})}]);return items}
function pendingFolderOptionsHTML(exclude=''){
  const node=(f,depth)=>f.ID===exclude?'':`<option value="${f.ID}">${'　'.repeat(depth)}${esc(f.Name)}</option>`+pendingChildren(f.ID).map(ch=>node(ch,depth+1)).join('');
  return pendingRoots().map(r=>node(r,0)).join('');
}
function movePendingFolderModal(id){const f=pendingFolder(id);showModal('移动文件夹',`<p class="meta">将「${esc(f?.Name||'')}」连同其中的子文件夹和用例移动到：</p><label>目标文件夹<select name="TargetFolderID">${pendingFolderOptionsHTML(id)}</select></label>`,x=>actReview('movePendingFolder',{FolderID:id,TargetFolderID:x.TargetFolderID}))}
function reviewCaseMenu(id){return [['查看详情',()=>setReqFocus({type:'reviewCase',id})],['编辑',()=>{const c=state.pendingCases.find(x=>x.ID===id);startCaseEdit(c,true,'case');setReqFocus({type:'reviewCase',id})}],['导入到版本…',()=>importReviewModal([id],state.pendingCases.find(x=>x.ID===id)?.FolderID)],['删除',()=>{if(confirm('确定删除这条待评审用例？'))actReview('deletePendingCase',{CaseID:id})}],['评审通过',()=>actReview('reviewPendingCase',{CaseID:id,Review:'passed'})],['评审不通过',()=>actReview('reviewPendingCase',{CaseID:id,Review:'rejected'})]]}
function pendingFolderModal(parent){showModal('新建文件夹',`<label>文件夹名称<input name="Name" required></label>`,x=>actReview('createPendingFolder',{...x,ParentID:parent}))}
function renamePendingFolderModal(id){const f=pendingFolder(id);showModal('重命名文件夹',`<label>文件夹名称<input name="Name" required value="${esc(f.Name)}"></label>`,x=>actReview('renamePendingFolder',{...x,FolderID:id}))}
function pendingCaseModal(c,folderId){folderId=c?.FolderID||folderId;showModal(c?'编辑待评审用例':'新增待评审用例',`<label>标题<input name="Title" required value="${esc(c?.Title||'')}"></label><label>优先级<select name="Priority">${['P0','P1','P2','P3'].map(x=>`<option ${c?.Priority===x?'selected':''}>${x}</option>`).join('')}</select></label><label>用例描述（评审时人工填写，可选）<textarea name="Description">${esc(c?.Description||'')}</textarea></label><label>前置条件<textarea name="Preconditions">${esc(c?.Preconditions||'')}</textarea></label><label>执行步骤<textarea name="Steps">${esc(c?.Steps||'')}</textarea></label><label>预期结果<textarea name="Expected">${esc(c?.Expected||'')}</textarea></label>`,x=>actReview(c?'editPendingCase':'createPendingCase',{...x,FolderID:folderId,CaseID:c?.ID||''}))}
// 导入到版本：可选目标版本和父目录（默认"全部用例"）。sourceFolderID 是评审区里"映射到父目录"的那一层，
// 它下面的子目录原样重建：评审区 A/B/C/D 勾选 B 导入时 sourceFolderID=A，结果为 <父目录>/B/C/D；
// 单条用例用其所在文件夹作来源，直接放进父目录。
const pendingParentOf=id=>id==='pending-root'?'pending-root':(pendingFolder(id)?.ParentID||'pending-root');
function pendingPathNames(fromExclusive,folderID){
  const names=[];
  for(let id=folderID,guard=0;id&&id!==fromExclusive&&guard<200;guard++){const f=pendingFolder(id);if(!f||f.ID==='pending-root')break;names.unshift(f.Name);id=f.ParentID}
  return names;
}
function folderPathNames(versionID,folderID){
  const names=[];
  for(let id=folderID,guard=0;id&&guard<200;guard++){const f=state.folders.find(x=>x.VersionID===versionID&&x.ID===id);if(!f)break;names.unshift(f.Name);id=f.ParentID}
  return names;
}
function importReviewModal(caseIds,sourceFolderID='pending-root'){
  if(!caseIds.length)return toast('该范围内没有待评审用例',true);
  const unreviewed=state.pendingCases.filter(c=>caseIds.includes(c.ID)&&c.Review!=='passed');
  if(unreviewed.length)return toast(`还有 ${unreviewed.length} 条用例未通过评审，无法导入`,true);
  const branches=state.versions.filter(v=>!v.mainline);
  if(!branches.length)return toast('请先创建一个测试版本作为导入目标',true);
  const sample=state.pendingCases.find(c=>c.ID===caseIds[0]);
  showModal('导入到版本',`<label>目标版本<select name="VersionID">${branches.map(v=>`<option value="${v.id}">${esc(v.name)}</option>`).join('')}</select></label><label>父目录<select name="TargetFolderID"></select></label><p class="meta">将导入 ${caseIds.length} 条已评审通过的用例，保持目录结构放到所选父目录下。</p><p class="meta" id="import-path-preview"></p>`,
    x=>act('importPendingCases',{VersionID:x.VersionID,TargetFolderID:x.TargetFolderID,SourceFolderID:sourceFolderID,CaseIDs:caseIds}).then(()=>{reqFocus=null;renderReqFocus();toast('已导入到目标版本')}));
  const vSel=$('#modal-body [name="VersionID"]'),fSel=$('#modal-body [name="TargetFolderID"]');
  const preview=()=>{$('#import-path-preview').textContent=sample?`例如「${sample.Title}」将放在：${[...folderPathNames(vSel.value,fSel.value),...pendingPathNames(sourceFolderID,sample.FolderID)].join(' / ')}`:''};
  const fill=()=>{fSel.innerHTML=folderOptionsHTML(vSel.value);fSel.value='root';preview()};
  vSel.onchange=fill;fSel.onchange=preview;fill();
}
$$('#req-sidebar [data-reqview]').forEach(b=>b.onclick=()=>{
  $$('#req-sidebar [data-reqview]').forEach(x=>x.classList.toggle('active',x===b));
  reqPage=b.dataset.reqview;
  $('#req-doc-view').classList.toggle('hidden',reqPage!=='docs');
  $('#req-review-view').classList.toggle('hidden',reqPage!=='review');
  updateReqEmptyHint();
});

function updateReqEmptyHint(){
  const collapsed=$('#req-sidebar').classList.contains('hidden'),review=reqPage==='review';
  $('#req-empty-title').textContent=collapsed?'展开侧栏，继续浏览':review?'从一条待评审用例开始':'从一个需求文档开始';
  $('#req-empty-hint').textContent=collapsed?'还没有选择要查看的内容。展开侧栏后，选择文件夹或需求文档即可查看详情。':review?'选择左侧的文件夹或待评审用例，即可在这里查看详情。':'选择左侧的文件夹或需求文档，即可在这里查看详情。';
  $('#req-empty-expand').textContent=review?'展开用例评审树':'展开需求树';
  $('#req-empty-expand').classList.toggle('hidden',!collapsed);
}
function setReqSidebarCollapsed(collapsed){
  if(collapsed&&!$('#req-sidebar').classList.contains('hidden')){
    const width=$('#req-sidebar').getBoundingClientRect().width;
    if(width>=220)reqSidebarWidth=width;
  }
  document.documentElement.style.setProperty('--req-side',collapsed?'0px':`${reqSidebarWidth||340}px`);
  $('#req-workspace').classList.toggle('sidebar-collapsed',collapsed);
  $('#req-sidebar').classList.toggle('hidden',collapsed);
  $('#req-resize-left').classList.toggle('hidden',collapsed);
  $('#req-expand').classList.toggle('hidden',!collapsed);
  updateReqEmptyHint();
}
$('#req-collapse').onclick=()=>setReqSidebarCollapsed(true);
$('#req-expand').onclick=$('#req-empty-expand').onclick=()=>setReqSidebarCollapsed(false);

// ---- Page tab bar (用例管理 / 需求管理) ----
function setPage(p){
  page=p;
  $$('.page-tab').forEach(b=>{const active=b.dataset.app===p;b.classList.toggle('active',active);b.setAttribute('aria-selected',String(active))});
  $('#app-subtitle').textContent={requirements:'需求文档管理',automation:'自动化脚本管理'}[p]||'测试用例管理';
  $('#workspace').classList.toggle('hidden',p!=='cases');
  $('#req-workspace').classList.toggle('hidden',p!=='requirements');
  $('#auto-workspace').classList.toggle('hidden',p!=='automation');
  if(p==='requirements')updateReqEmptyHint();
  if(p==='automation')updateAutoEmptyHint();
}
$$('.page-tab').forEach(b=>b.onclick=()=>setPage(b.dataset.app));

// ---- 自动化管理（脚本树）----------------------------------------------------
// 脚本与用例一一对应，按 (版本, 用例) 存在 state.scripts 里。这里不维护第二套目录：
// 目录结构直接取用例当前所在的文件夹，所以两棵树天然同名同构，用例移动/改名后脚本树
// 自动跟随。树上只显示已经生成过脚本的用例，空目录不出现。
let autoFocus=null, autoSidebarWidth=null;
const scriptsIn=vid=>state.scripts.filter(s=>s.VersionID===vid);
const scriptFor=(vid,caseID)=>state.scripts.find(s=>s.VersionID===vid&&s.CaseID===caseID);
const caseOfScript=s=>state.cases.find(c=>c.VersionID===s.VersionID&&c.ID===s.CaseID);
// 用例内容改动后，脚本断言的就不再是当前用例：FromXxx 是生成时的用例原文快照，
// 与当前用例逐字段比较即可判断，和"阅读友好版"用的是同一套机制。
function scriptStale(s){const c=caseOfScript(s);return !!c&&(s.FromPreconditions!==(c.Preconditions||'')||s.FromSteps!==(c.Steps||'')||s.FromExpected!==(c.Expected||''))}
const scriptStatusName=s=>s.Status==='blocked'?'未生成（受阻）':'已生成';
function scriptBadges(s){
  return `${s.Status==='blocked'?'<span class="result failed" title="受阻未生成"></span>':'<span class="result passed" title="已生成"></span>'}${scriptStale(s)?'<span class="risk-tag risk-medium script-stale-tag" title="用例内容已变更">过时</span>':''}`;
}
function scriptTreeHTML(vid){
  const fs=folders(vid),cs=cases(vid),list=scriptsIn(vid);
  if(!list.length)return '';
  const byFolder=new Map();
  for(const s of list){
    const c=cs.find(x=>x.ID===s.CaseID),fid=c?c.FolderID:'root';
    if(!byFolder.has(fid))byFolder.set(fid,[]);
    byFolder.get(fid).push(s);
  }
  const has=f=>(byFolder.get(f.ID)?.length||0)>0||fs.filter(x=>x.ParentID===f.ID).some(has);
  function node(f,depth){
    if(!has(f))return '';
    const own=byFolder.get(f.ID)||[],children=fs.filter(x=>x.ParentID===f.ID);
    return `<div class="tree-row folder-row" style="padding-left:${8+depth*17}px" data-script-folder="${f.ID}" data-script-version="${vid}"><span class="chev">▾</span><span>📁</span><span class="label">${esc(f.Name)}</span></div><div>${own.map(s=>scriptRow(s,depth+1)).join('')}${children.map(ch=>node(ch,depth+1)).join('')}</div>`;
  }
  const roots=fs.filter(f=>!f.ParentID||!fs.some(x=>x.ID===f.ParentID));
  return roots.map(r=>node(r,0)).join('');
}
function scriptRow(s,depth){
  const label=`${s.CaseID} ${s.Title||caseOfScript(s)?.Title||''}`;
  return `<div class="tree-row case-row" style="padding-left:${8+depth*17}px" data-script-case="${s.CaseID}" data-script-version="${s.VersionID}" title="${esc(label)}"><span class="label">${esc(label)}</span>${scriptBadges(s)}</div>`;
}
function renderScriptTree(){
  const box=$('#script-tree');
  if(!box)return;
  const withScripts=state.versions.filter(v=>scriptsIn(v.id).length);
  box.innerHTML=withScripts.map(v=>`<div class="version" data-version="${v.id}"><div class="version-title"><span class="chev">⌄</span><span>${esc(v.name)}</span><span class="badge">${scriptsIn(v.id).length} 个脚本</span></div><div class="version-body">${scriptTreeHTML(v.id)}</div></div>`).join('')
    ||'<p class="meta script-tree-empty">还没有生成脚本。在「用例管理」里右键目录或用例，选择「脚本生成」，生成结果会自动同步到这里。</p>';
  bindScriptTree(box);
}
function bindScriptTree(root){
  root.querySelectorAll('.version-title').forEach(e=>{
    const v=e.parentElement,key=`script:${v.dataset.version}`;
    if(closedVersions.has(key))v.classList.add('closed');
    e.onclick=()=>{const closed=v.classList.toggle('closed');closed?closedVersions.add(key):closedVersions.delete(key)};
  });
  root.querySelectorAll('[data-script-folder]').forEach(e=>{
    const key=`script:${e.dataset.scriptVersion}:${e.dataset.scriptFolder}`;
    if(closedFolders.has(key))e.classList.add('closed');
    e.querySelector('.chev').onclick=x=>{x.stopPropagation();const closed=e.classList.toggle('closed');closed?closedFolders.add(key):closedFolders.delete(key)};
    e.onclick=x=>{if(x.target.closest('.chev'))return;setAutoFocus({type:'folder',versionID:e.dataset.scriptVersion,id:e.dataset.scriptFolder})};
    e.oncontextmenu=x=>menu(x,scriptFolderMenu(e.dataset.scriptVersion,e.dataset.scriptFolder));
  });
  root.querySelectorAll('[data-script-case]').forEach(e=>{
    e.onclick=()=>setAutoFocus({type:'script',versionID:e.dataset.scriptVersion,id:e.dataset.scriptCase});
    e.oncontextmenu=x=>menu(x,scriptMenu(e.dataset.scriptVersion,e.dataset.scriptCase));
  });
}
// 目录下（含子目录）已有脚本的用例，用于按目录重新生成/删除。
function scriptCaseIdsIn(vid,folderId){
  const f=state.folders.find(x=>x.VersionID===vid&&x.ID===folderId);
  if(!f)return [];
  const scope=[f.ID,...descendantFolders(f)];
  return scriptsIn(vid).filter(s=>{const c=caseOfScript(s);return c&&scope.includes(c.FolderID)}).map(s=>s.CaseID);
}
function scriptFolderMenu(vid,folderId){
  const ids=scriptCaseIdsIn(vid,folderId);
  return [['查看详情',()=>setAutoFocus({type:'folder',versionID:vid,id:folderId})],
    [`重新生成（${ids.length}）`,()=>ids.length?startGeneration(vid,ids,`重新生成 · ${state.folders.find(x=>x.VersionID===vid&&x.ID===folderId)?.Name||''}`):toast('该目录下没有脚本',true)],
    ['在用例管理中打开',()=>{setPage('cases');setFocus({type:'folder',versionID:vid,id:folderId})}],
    ['删除目录下的脚本',()=>{if(ids.length&&confirm(`确定删除该目录下的 ${ids.length} 个脚本？用例本身不受影响，可以重新生成。`))act('deleteScripts',{VersionID:vid,CaseIDs:ids}).then(()=>{if(autoFocus?.type==='script')autoFocus=null;toast('脚本已删除')}).catch(()=>{})},'danger']];
}
function scriptMenu(vid,caseID){
  return [['查看详情',()=>setAutoFocus({type:'script',versionID:vid,id:caseID})],
    ['重新生成',()=>startGeneration(vid,[caseID],caseID)],
    ['在用例管理中打开',()=>openCaseFromScript(vid,caseID)],
    ['删除脚本',()=>{if(confirm('确定删除这个脚本？用例本身不受影响，可以重新生成。'))act('deleteScripts',{VersionID:vid,CaseIDs:[caseID]}).then(()=>{if(autoFocus?.id===caseID)autoFocus=null;toast('脚本已删除')}).catch(()=>{})},'danger']];
}
function openCaseFromScript(vid,caseID){
  if(!state.cases.some(c=>c.VersionID===vid&&c.ID===caseID))return toast('对应用例已不存在',true);
  setPage('cases');setFocus({type:'case',versionID:vid,id:caseID});
}
function setAutoFocus(x){autoFocus=x;renderScriptTree();renderAutoFocus()}
function renderAutoFocus(){
  const box=$('#auto-detail');
  if(!box)return;
  if(!autoFocus){updateAutoEmptyHint();$('#auto-empty').classList.remove('hidden');box.classList.add('hidden');return}
  if(autoFocus.type==='folder'){
    const f=state.folders.find(x=>x.VersionID===autoFocus.versionID&&x.ID===autoFocus.id);
    if(!f){autoFocus=null;return renderAutoFocus()}
    const ids=scriptCaseIdsIn(f.VersionID,f.ID),scripts=ids.map(id=>scriptFor(f.VersionID,id));
    $('#auto-empty').classList.add('hidden');box.classList.remove('hidden');
    box.innerHTML=`<div class="detail-head"><div><div class="eyebrow">脚本目录 · ${esc(version(f.VersionID)?.name||'')}</div><h1>📁 ${esc(f.Name)}</h1></div><div class="detail-actions"><button id="auto-folder-regen"${ids.length?'':' disabled'}>重新生成全部</button></div></div><div class="card meta-grid"><span>脚本 <b>${ids.length}</b></span><span>已生成 <b>${scripts.filter(s=>s.Status!=='blocked').length}</b></span><span>受阻 <b>${scripts.filter(s=>s.Status==='blocked').length}</b></span><span>已过时 <b>${scripts.filter(scriptStale).length}</b></span></div>`;
    const btn=$('#auto-folder-regen');
    if(btn)btn.onclick=()=>startGeneration(f.VersionID,ids,`重新生成 · ${f.Name}`);
    return;
  }
  const s=scriptFor(autoFocus.versionID,autoFocus.id);
  if(!s){autoFocus=null;return renderAutoFocus()}
  $('#auto-empty').classList.add('hidden');box.classList.remove('hidden');
  const c=caseOfScript(s),stale=scriptStale(s),blocked=s.Status==='blocked';
  const deviations=s.Deviations?.length?`<div class="card"><h3>与用例预期的实测偏差 <small class="meta">脚本按实测行为断言，并在对应行标注 // deviation:</small></h3>${aiRiskListHTML(s.Deviations.map(d=>({risk:d.Risk,summary:d.Summary})),d=>esc(d.summary))}</div>`:'';
  const staleHint=stale?`<div class="card script-stale"><b>用例内容已变更</b><p class="meta">这个脚本是按变更前的用例生成的，断言可能已经不符合当前用例。确认用例后可以重新生成。</p><p class="drawer-actions"><button type="button" id="script-regen-stale">重新生成</button></p></div>`:'';
  const body=blocked
    ?`<div class="card"><h3>未能生成脚本</h3><p>${esc(s.Summary||'生成器未说明原因')}</p><p class="meta">生成器在缺少必需输入（账号、令牌、素材等）或流程不可达时不会写出脚本，也不会用 skip/占位断言绕过。补齐所需输入后重新生成即可。</p></div>`
    :`<div class="card script-code-card"><div class="script-code-head"><b>${esc(s.FileName)}</b><span class="meta">${s.Code.split('\n').length} 行</span><button type="button" class="secondary" id="script-copy">复制</button><button type="button" class="secondary" id="script-download">下载</button></div><pre class="script-code">${esc(s.Code)}</pre></div>`;
  box.innerHTML=`<div class="detail-head"><div><div class="eyebrow">${esc(s.CaseID)} · ${esc(version(s.VersionID)?.name||'')}</div><h1>${esc(s.Title||c?.Title||s.CaseID)}</h1></div><div class="detail-actions"><button class="secondary" id="script-open-case">查看用例</button><button id="script-regen">重新生成</button></div></div><div class="card meta-grid"><span>状态 <b>${scriptStatusName(s)}${stale?' · 已过时':''}</b></span><span>文件 <b>${esc(s.FileName)}</b></span><span>更新时间 <b>${fmt(s.UpdatedAt)}</b></span><span>生成者 <b>${esc(s.UpdatedBy||'—')}</b></span></div>${s.Summary&&!blocked?`<div class="card"><h3>脚本验证的内容</h3><p>${esc(s.Summary)}</p></div>`:''}${staleHint}${deviations}${body}`;
  $('#script-open-case').onclick=()=>openCaseFromScript(s.VersionID,s.CaseID);
  const regen=()=>startGeneration(s.VersionID,[s.CaseID],s.CaseID);
  $('#script-regen').onclick=regen;
  $('#script-regen-stale')?.addEventListener('click',regen);
  $('#script-copy')?.addEventListener('click',async()=>{
    try{await navigator.clipboard.writeText(s.Code);toast('脚本已复制')}catch{toast('复制失败，请手动选择复制',true)}
  });
  $('#script-download')?.addEventListener('click',()=>{
    const a=document.createElement('a');
    a.href=URL.createObjectURL(new Blob([s.Code],{type:'text/plain;charset=utf-8'}));
    a.download=s.FileName;a.click();URL.revokeObjectURL(a.href);
  });
}
function updateAutoEmptyHint(){
  const collapsed=$('#auto-sidebar').classList.contains('hidden');
  $('#auto-empty-title').textContent=collapsed?'展开侧栏，继续浏览':'从一个脚本开始';
  $('#auto-empty-expand').classList.toggle('hidden',!collapsed);
}
function setAutoSidebarCollapsed(collapsed){
  if(collapsed&&!$('#auto-sidebar').classList.contains('hidden')){
    const width=$('#auto-sidebar').getBoundingClientRect().width;
    if(width>=220)autoSidebarWidth=width;
  }
  document.documentElement.style.setProperty('--auto-side',collapsed?'0px':`${autoSidebarWidth||340}px`);
  $('#auto-workspace').classList.toggle('sidebar-collapsed',collapsed);
  $('#auto-sidebar').classList.toggle('hidden',collapsed);
  $('#auto-resize-left').classList.toggle('hidden',collapsed);
  $('#auto-expand').classList.toggle('hidden',!collapsed);
  updateAutoEmptyHint();
}
$('#auto-collapse').onclick=()=>setAutoSidebarCollapsed(true);
$('#auto-expand').onclick=$('#auto-empty-expand').onclick=()=>setAutoSidebarCollapsed(false);

// ---- 脚本生成抽屉（对接 auto-test generator HTTP 服务）------------------------
// 与"AI 设计"抽屉的单任务模型不同：脚本生成天然是批量的（一个目录几十条用例），
// 所以抽屉里是一个任务列表，每个任务一个可展开选项卡，可以同时跑多个、关掉抽屉后
// 继续在后台跑，重新打开按 ID 恢复查看。任务清单存在 localStorage，脚本本身存在
// 服务端 state 里（saveScript）。
const GEN_TASKS_KEY='casehub-gen-tasks';
// 上次填过的目标信息，下次生成默认带出；密码不保存，每次重新输入。
const GEN_TARGET_KEY='casehub-gen-target';
const GEN_MAX_CASES=50; // generator 单任务上限，超出自动拆成多个任务
const GEN_TERMINAL=['succeeded','failed','cancelled'];
let genTasks=loadGenTasks(), genDraft=null, genEnabled=null;
const genRuntime=new Map(); // jobId -> {source, log:[], stage, status}
const genOpen=new Set();    // 展开的选项卡（'draft' 或 jobId）
function loadGenTasks(){try{const x=JSON.parse(localStorage.getItem(GEN_TASKS_KEY));return Array.isArray(x)?x:[]}catch{return []}}
function saveGenTasks(){try{localStorage.setItem(GEN_TASKS_KEY,JSON.stringify(genTasks.slice(0,30)))}catch{}}
function loadGenTarget(){try{return JSON.parse(localStorage.getItem(GEN_TARGET_KEY))||{}}catch{return {}}}
function saveGenTarget(v){try{localStorage.setItem(GEN_TARGET_KEY,JSON.stringify({baseUrl:v.baseUrl||'',instructions:v.instructions||'',testAccount:v.testAccount||'',reqDoc:v.reqDoc||'auto'}))}catch{}}
const isGenDrawerOpen=()=>!$('#gen-drawer').classList.contains('hidden');
const genTask=jobId=>genTasks.find(t=>t.jobId===jobId);
const genRunning=()=>genTasks.filter(t=>!GEN_TERMINAL.includes(t.status));

function generateForFolder(vid,folderId){
  const f=state.folders.find(x=>x.VersionID===vid&&x.ID===folderId);
  if(!f)return;
  const scope=[f.ID,...descendantFolders(f)];
  const ids=cases(vid).filter(c=>scope.includes(c.FolderID)).map(c=>c.ID);
  if(!ids.length)return toast('该目录没有用例',true);
  startGeneration(vid,ids,f.Name);
}
// 生成的输入是用例本身（planner 设计、评审后导入的那份原文），可选附带需求文档做背景。
function startGeneration(vid,caseIDs,label){
  const usable=caseIDs.filter(id=>{const c=state.cases.find(x=>x.VersionID===vid&&x.ID===id);return c&&(c.Steps||'').trim()});
  if(!usable.length)return toast('选中的用例没有执行步骤，无法生成脚本',true);
  genDraft={versionID:vid,caseIDs:usable,skipped:caseIDs.length-usable.length,label:label||`${usable.length} 条用例`};
  genOpen.add('draft');
  openGenDrawer();
}
function openGenDrawer(){
  $('#gen-drawer').classList.remove('hidden');
  renderGenDrawer();
}
$('#gen-drawer-close').onclick=()=>$('#gen-drawer').classList.add('hidden');

async function renderGenDrawer(){
  const body=$('#gen-drawer-body');
  if(genEnabled===null){
    body.innerHTML='<p class="meta">正在检查脚本生成服务…</p>';
    try{genEnabled=(await serviceRequest('/api/generator/status')).enabled}catch{genEnabled=false}
  }
  $('#gen-drawer-sub').textContent=genRunning().length?`${genRunning().length} 个任务进行中`:`${genTasks.length} 个任务`;
  if(!genEnabled){
    body.innerHTML='<p class="meta">脚本生成服务未配置（缺少 CASEHUB_GENERATOR_URL），暂时无法使用。</p>';
    return;
  }
  body.innerHTML=`${genDraft?genDraftHTML():''}${genTasks.length?genTasks.map(genTaskHTML).join(''):(genDraft?'':'<p class="meta">还没有生成任务。在「用例管理」里右键目录或用例，选择「脚本生成」。</p>')}`;
  bindGenDraft();
  bindGenTasks();
}
const genCaseCount=t=>t.caseIDs.length;
function genDraftHTML(){
  const v=loadGenTarget(),d=genDraft;
  const existing=d.caseIDs.filter(id=>scriptFor(d.versionID,id)).length;
  const batches=Math.ceil(d.caseIDs.length/GEN_MAX_CASES);
  const docs=state.reqDocs.map(x=>`<option value="${x.ID}"${v.reqDoc===x.ID?' selected':''}>${esc(x.Title)}${x.Code?`（${esc(x.Code)}）`:''}</option>`).join('');
  return `<details class="gen-task" data-gen-panel="draft"${genOpen.has('draft')?' open':''}>
    <summary><span class="gen-task-title">新建生成任务 · ${esc(d.label)}</span><span class="gen-chip">${d.caseIDs.length} 条用例</span></summary>
    <form id="gen-form" autocomplete="off">
      <p class="meta">脚本按用例原文（前置条件 / 步骤 / 预期结果）生成，一条用例一个 spec 文件，结果自动同步到「自动化管理」。${existing?`其中 ${existing} 条已有脚本，会被覆盖。`:''}${d.skipped?`已跳过 ${d.skipped} 条没有执行步骤的用例。`:''}${batches>1?`超过单任务上限，将拆成 ${batches} 个任务依次提交。`:''}</p>
      <label>被测系统 URL<input name="baseUrl" required placeholder="https://test.example.com" value="${esc(v.baseUrl||'')}"></label>
      <label>参考需求文档<select name="reqDoc"><option value="auto"${(v.reqDoc||'auto')==='auto'?' selected':''}>自动匹配（按用例编号前缀）</option><option value=""${v.reqDoc===''?' selected':''}>不附带需求文档</option>${docs}</select></label>
      <label>补充说明（可选）<textarea name="instructions" placeholder="登录方式、数据约束、需要避免的操作等">${esc(v.instructions||'')}</textarea></label>
      <label>测试账号 · 用户名（可选）<input name="testAccount" autocomplete="off" value="${esc(v.testAccount||'')}"></label>
      <label>测试账号 · 密码（可选）<input name="testSecret" type="text" class="fake-password" autocomplete="off" spellcheck="false"></label>
      <p class="drawer-actions"><button type="button" class="secondary" id="gen-cancel-draft">取消</button><button type="submit">开始生成</button></p>
    </form>
  </details>`;
}
function bindGenDraft(){
  const form=$('#gen-form');
  if(!form)return;
  $('#gen-cancel-draft').onclick=()=>{genDraft=null;genOpen.delete('draft');renderGenDrawer()};
  form.onsubmit=async e=>{
    e.preventDefault();
    const values=Object.fromEntries(new FormData(form));
    if(!values.baseUrl.trim())return toast('请填写被测系统 URL',true);
    saveGenTarget(values);
    const btn=form.querySelector('button[type="submit"]');btn.disabled=true;
    const d=genDraft,chunks=[];
    for(let i=0;i<d.caseIDs.length;i+=GEN_MAX_CASES)chunks.push(d.caseIDs.slice(i,i+GEN_MAX_CASES));
    try{
      for(const [i,ids] of chunks.entries()){
        const label=chunks.length>1?`${d.label}（${i+1}/${chunks.length}）`:d.label;
        await submitGenJob(d.versionID,ids,label,values);
      }
      genDraft=null;genOpen.delete('draft');
      toast(chunks.length>1?`已提交 ${chunks.length} 个生成任务`:'生成任务已提交');
    }catch(err){toast(err.message,true);btn.disabled=false}
    renderGenDrawer();
  };
}
// 用例编号 TC-<需求缩写>-… 的第二段就是需求缩写，用它自动匹配需求文档；匹配不到就不附带。
function reqDocForCase(c){
  const m=/^TC-([A-Z][A-Z0-9]{1,11})-/.exec(c.ID||'');
  return m?state.reqDocs.find(d=>d.Code===m[1]):undefined;
}
function genPayload(vid,ids,values){
  const picked=ids.map(id=>state.cases.find(c=>c.VersionID===vid&&c.ID===id)).filter(Boolean);
  const docs=new Map();
  const cases=picked.map(c=>{
    const doc=values.reqDoc==='auto'?reqDocForCase(c):(values.reqDoc?state.reqDocs.find(d=>d.ID===values.reqDoc):undefined);
    if(doc&&(doc.Content||'').trim())docs.set(doc.ID,{id:doc.ID,title:doc.Title,content:doc.Content});
    return {id:c.ID,title:c.Title,priority:c.Priority||'',...(doc&&docs.has(doc.ID)?{requirement:doc.ID}:{}),
      precondition:c.Preconditions||'',steps:c.Steps||'',expects:c.Expected||''};
  });
  const payload={cases,target:{baseUrl:values.baseUrl.trim()},context:{}};
  if(docs.size)payload.requirements=[...docs.values()];
  if((values.instructions||'').trim())payload.context.instructions=values.instructions.trim();
  if(values.testAccount||values.testSecret)payload.context.testData={username:values.testAccount||'',password:values.testSecret||''};
  return payload;
}
async function submitGenJob(vid,ids,label,values){
  const job=await serviceRequest('/api/generator/jobs',{method:'POST',headers:{'Content-Type':'application/json'},
    body:JSON.stringify(genPayload(vid,ids,values))});
  const task={jobId:job.id,versionID:vid,versionName:version(vid)?.name||'',caseIDs:ids,label,createdAt:job.createdAt||new Date().toISOString(),status:job.status,stage:job.stage};
  genTasks.unshift(task);saveGenTasks();
  genOpen.add(job.id);
  routeGenJob(task,job);
}
function genStatusText(task){
  const status={queued:'排队中',running:'进行中',succeeded:'已完成',failed:'失败',cancelled:'已取消',importing:'正在保存脚本'}[task.status]||task.status;
  return task.stage&&!GEN_TERMINAL.includes(task.status)?`${status} · ${esc(task.stage)}`:status;
}
function genTaskHTML(task){
  const runtime=genRuntime.get(task.jobId);
  const done=task.saved?`<div class="card meta-grid"><span>已生成 <b>${task.saved.generated}</b></span><span>受阻 <b>${task.saved.blocked}</b></span>${task.saved.failed?`<span>保存失败 <b>${task.saved.failed}</b></span>`:''}</div>`:'';
  const limitations=task.limitations?.length?`<div class="card"><h3>未生成的用例 <small class="meta">按风险从高到低</small></h3>${aiRiskListHTML(task.limitations,l=>esc(l.summary))}</div>`:'';
  const error=task.error?`<p class="meta">${esc(task.error.code||'')}${task.error.code?'：':''}${esc(task.error.message||'')}</p>`:'';
  const actions=GEN_TERMINAL.includes(task.status)
    ?`<button type="button" class="secondary" data-gen-forget="${task.jobId}">移除记录</button>${task.status==='succeeded'&&!task.saved?`<button type="button" data-gen-import="${task.jobId}">重试保存</button>`:''}${task.status!=='succeeded'?`<button type="button" data-gen-retry="${task.jobId}">重新生成</button>`:''}`
    :`<button type="button" class="secondary" data-gen-cancel="${task.jobId}">取消任务</button>`;
  return `<details class="gen-task" data-gen-panel="${task.jobId}"${genOpen.has(task.jobId)?' open':''}>
    <summary><span class="gen-task-title">${esc(task.label)}</span><span class="gen-chip">${genCaseCount(task)} 条</span><span class="gen-chip gen-status-${GEN_TERMINAL.includes(task.status)?task.status:'running'}" data-gen-status="${task.jobId}">${genStatusText(task)}</span></summary>
    <p class="meta">${esc(task.versionName||'')} · 提交于 ${fmt(task.createdAt)}</p>
    ${done}${limitations}${error}
    <div class="ai-log" data-gen-log="${task.jobId}">${(runtime?.log||[]).map(line=>`<div>${esc(line)}</div>`).join('')}</div>
    <p class="drawer-actions">${actions}</p>
  </details>`;
}
function bindGenTasks(){
  $$('#gen-drawer-body [data-gen-panel]').forEach(el=>{
    el.ontoggle=()=>{el.open?genOpen.add(el.dataset.genPanel):genOpen.delete(el.dataset.genPanel)};
  });
  $$('#gen-drawer-body [data-gen-cancel]').forEach(b=>b.onclick=async()=>{
    b.disabled=true;
    try{await serviceRequest(`/api/generator/jobs/${b.dataset.genCancel}`,{method:'DELETE'})}catch(e){toast(e.message,true);b.disabled=false}
  });
  $$('#gen-drawer-body [data-gen-import]').forEach(b=>b.onclick=()=>{const t=genTask(b.dataset.genImport);if(t)importGenResult(t)});
  $$('#gen-drawer-body [data-gen-retry]').forEach(b=>b.onclick=()=>{const t=genTask(b.dataset.genRetry);if(t)startGeneration(t.versionID,t.caseIDs,t.label)});
  $$('#gen-drawer-body [data-gen-forget]').forEach(b=>b.onclick=()=>{
    const id=b.dataset.genForget;
    closeGenStream(id);
    genTasks=genTasks.filter(t=>t.jobId!==id);saveGenTasks();genRuntime.delete(id);genOpen.delete(id);
    renderGenDrawer();
  });
}
function genLogLine(jobId,msg){
  const runtime=genRuntime.get(jobId)||{log:[]};
  runtime.log.push(msg);
  if(runtime.log.length>200)runtime.log.shift();
  genRuntime.set(jobId,runtime);
  const box=$(`#gen-drawer-body [data-gen-log="${jobId}"]`);
  if(!box)return;
  const atBottom=box.scrollTop+box.clientHeight>=box.scrollHeight-4;
  const line=document.createElement('div');line.textContent=msg;box.appendChild(line);
  while(box.childElementCount>200)box.removeChild(box.firstChild);
  if(atBottom)box.scrollTop=box.scrollHeight;
}
function updateGenStatusChip(task){
  const chip=$(`#gen-drawer-body [data-gen-status="${task.jobId}"]`);
  if(chip){chip.textContent=genStatusText(task);chip.className=`gen-chip gen-status-${GEN_TERMINAL.includes(task.status)?task.status:'running'}`}
  $('#gen-drawer-sub').textContent=genRunning().length?`${genRunning().length} 个任务进行中`:`${genTasks.length} 个任务`;
}
function closeGenStream(jobId){const r=genRuntime.get(jobId);r?.source?.close();if(r)r.source=null}
// 任务状态的唯一分发点：抽屉关不关都会更新任务列表、维持 SSE，并在完成时自动保存脚本。
function routeGenJob(task,job){
  task.status=job.status;task.stage=job.stage;task.error=job.error;
  saveGenTasks();
  updateGenStatusChip(task);
  if(!GEN_TERMINAL.includes(job.status))return ensureGenStream(task);
  closeGenStream(task.jobId);
  if(job.status==='succeeded'&&!task.saved)return importGenResult(task);
  if(isGenDrawerOpen())renderGenDrawer();
}
function ensureGenStream(task){
  const runtime=genRuntime.get(task.jobId)||{log:[]};
  if(runtime.source)return;
  const source=new EventSource(`/api/generator/jobs/${task.jobId}/events`);
  runtime.source=source;genRuntime.set(task.jobId,runtime);
  source.addEventListener('snapshot',e=>{
    const job=JSON.parse(e.data);
    task.status=job.status;task.stage=job.stage;task.error=job.error;saveGenTasks();updateGenStatusChip(task);
    if(GEN_TERMINAL.includes(job.status))routeGenJob(task,job);
  });
  source.addEventListener('progress',e=>{
    const ev=JSON.parse(e.data);
    task.stage=ev.stage;updateGenStatusChip(task);
    genLogLine(task.jobId,`[${fmt(ev.createdAt)}] ${ev.stage||''} ${ev.message||''}${ev.tool?` (${ev.tool} ${ev.toolStatus||''})`:''}`.trim());
    if(GEN_TERMINAL.includes(ev.status))serviceRequest(`/api/generator/jobs/${task.jobId}`).then(job=>routeGenJob(task,job)).catch(()=>{});
  });
  source.addEventListener('reset',e=>{
    const r=JSON.parse(e.data);
    genLogLine(task.jobId,`……${r.message||'更早的进度记录已丢失'}`);
  });
  source.onerror=()=>{/* EventSource 会自己重连；终态时服务端关闭连接，routeGenJob 已经停止订阅 */};
}
// 生成完成后自动把脚本写进 CaseHub（saveScript），无需人工点击导入；受阻的用例也照样
// 保存，它带着"为什么没生成"的原因，正是评审人需要看到的。
async function importGenResult(task){
  if(task.importing)return;
  task.importing=true;task.status='importing';updateGenStatusChip(task);
  try{
    const result=await serviceRequest(`/api/generator/jobs/${task.jobId}/result`);
    let failed=0;
    for(const sc of result.scripts||[]){
      try{
        await act('saveScript',{VersionID:task.versionID,CaseID:sc.caseId,ScriptFileName:sc.fileName,ScriptLanguage:sc.language,
          ScriptCode:sc.code||'',ScriptStatus:sc.status,ScriptSummary:sc.summary||'',ScriptJobID:task.jobId,
          ScriptDeviations:(sc.deviations||[]).map(d=>({Risk:d.risk,Summary:d.summary}))});
      }catch{failed++} // act 已提示失败原因（例如用例在生成期间被删除）
    }
    task.status='succeeded';
    task.saved={generated:result.generated??0,blocked:result.blocked??0,failed};
    task.limitations=result.limitations||[];
    toast(`「${task.label}」生成完成：${task.saved.generated} 个脚本已保存${task.saved.blocked?`，${task.saved.blocked} 条用例未生成`:''}`);
  }catch(e){
    task.status='succeeded'; // 任务本身成功了，只是结果还没保存下来
    task.error={code:'SAVE_FAILED',message:`保存脚本失败：${e.message}`};
    toast(task.error.message,true);
  }finally{
    task.importing=false;saveGenTasks();
    if(isGenDrawerOpen())renderGenDrawer();
  }
}
// 刷新页面后恢复未完成的任务：按 ID 查状态，继续订阅或补做保存。
async function resumeGenTasks(){
  for(const task of genTasks){
    if(GEN_TERMINAL.includes(task.status)&&task.saved)continue;
    try{
      const job=await serviceRequest(`/api/generator/jobs/${task.jobId}`);
      routeGenJob(task,job);
    }catch(e){
      if(!GEN_TERMINAL.includes(task.status)){
        task.status='failed';task.error={code:e.code||'JOB_LOST',message:e.message||'任务状态已失效'};saveGenTasks();
      }
    }
  }
}
