const $=s=>document.querySelector(s), $$=s=>[...document.querySelectorAll(s)];
let state=null, focus=null, selected=new Map(), view='cases', modalSave=null, recordCase=null, recordTask='', recordEditor=null, recordViewers=[], recordHistoryLimit=3;
let openTasks=new Set(), closedTaskVersions=new Set(), closedFolders=new Set(), closedReqFolders=new Set(), closedReviewFolders=new Set(), closedVersions=new Set();
let page='cases', reqFocus=null, reqEditor=null, reqSidebarWidth=null, aiDoc=null, reqPage='docs';
let aiSource=null, aiPlannerEnabled=null;
let caseViewMode='friendly'; // 'friendly' | 'raw' — applies to whichever case detail is currently shown
const esc=s=>String(s??'').replace(/[&<>'"]/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;',"'":'&#39;','"':'&quot;'}[c]));
const fmt=s=>s?new Date(s).toLocaleString():'—';
const version=id=>state.versions.find(v=>v.id===id), folders=id=>state.folders.filter(f=>f.VersionID===id), cases=id=>state.cases.filter(c=>c.VersionID===id);
async function request(path,options){let r=await fetch(path,options),x=await r.json();if(!r.ok){let e=Error(x.error||'请求失败');e.status=r.status;e.conflicts=x.conflicts;throw e}return x}
function normalizeState(s){s=s||{};for(const key of ['versions','folders','cases','histories','records','tasks','reqFolders','reqDocs','pendingFolders','pendingCases'])if(!Array.isArray(s[key]))s[key]=[];return s}
async function refresh(){state=normalizeState(await request('/api/state'));render();}
async function act(type,data={},retry=false){try{let out=await request('/api/action',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({Type:type,Author:'本地用户',...data})});state=normalizeState(out.state);if(out.warnings?.length)toast(out.warnings.join('；'));render();return out}catch(e){if(e.status===409&&type.startsWith('merge')){alert(`${e.message}\n冲突用例：${(e.conflicts||[]).join(', ')}\n请拉取主线，然后打开冲突用例编辑并确认人工处理。`)}else if(e.status===409&&!retry&&confirm(`${e.message}\n冲突用例：${(e.conflicts||[]).join(', ')}\n是否以当前编辑内容作为人工解决结果？`))return act(type,{...data,Force:true},true);toast(e.message,true);throw e}}
function render(){ renderVersions();renderTasks();renderFocus();if(recordTask)$('#edit-case')?.remove();updateBulk();if(location.hash)renderHistoryRoute();renderReqTree();renderReviewTree(); }
function renderVersions(){let box=$('#versions');box.innerHTML=state.versions.map(v=>`<div class="version" data-version="${v.id}"><div class="version-title"><span class="chev">⌄</span><span>${esc(v.name)}</span><span class="badge">${v.mainline?'只读主线':'测试版本'}</span>${v.mainline?'':`<span class="version-actions"><button data-sync="${v.id}">拉取主线</button><button data-merge="${v.id}">合并</button><button data-delete-version="${v.id}" class="danger">删除</button></span>`}</div><div class="version-body">${tree(v.id)}</div></div>`).join('');bindTree(box);}
function tree(vid,onlyIDs=null,taskID=''){let fs=folders(vid),cs=cases(vid),roots=fs.filter(f=>!f.ParentID||!fs.some(x=>x.ID===f.ParentID));let branch=!version(vid).mainline,showCheck=branch&&!taskID;function node(f,depth){let children=fs.filter(x=>x.ParentID===f.ID),own=cs.filter(c=>c.FolderID===f.ID);let visible=!onlyIDs||own.some(c=>onlyIDs.has(c.ID))||children.some(ch=>hasHit(ch));if(!visible)return'';return `<div class="tree-row folder-row" style="padding-left:${8+depth*17}px" data-folder="${f.ID}" data-version="${vid}">${showCheck?'<input class="folder-check" type="checkbox">':''}<span class="chev">▾</span><span>📁</span><span class="label">${esc(f.Name)}</span></div><div>${own.filter(c=>!onlyIDs||onlyIDs.has(c.ID)).map(c=>caseRow(c,depth+1,branch&&!taskID,taskID)).join('')}${children.map(ch=>node(ch,depth+1)).join('')}</div>`}function hasHit(f){return cs.some(c=>c.FolderID===f.ID&&onlyIDs.has(c.ID))||fs.filter(x=>x.ParentID===f.ID).some(hasHit)}return roots.map(r=>node(r,0)).join('')||'<p class="meta">空版本</p>'}
function caseRow(c,depth,branch,taskID=''){let checked=selected.get(c.VersionID)?.has(c.ID);return `<div class="tree-row ${taskID?'task-case':'case-row'}" style="padding-left:${8+depth*17}px" data-case="${c.ID}" data-c="${c.ID}" data-version="${c.VersionID}" data-v="${c.VersionID}" ${taskID?`data-task="${taskID}"`:''}>${branch?`<input class="case-check" type="checkbox" ${checked?'checked':''}>`:''}<span class="label">${esc(c.Title)}</span>${c.Result?`<span class="result ${c.Result}"></span>`:''}</div>`}
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
function caseDetailBodyHTML(c){
  const toggle=caseViewToggleHTML();
  if(caseViewMode!=='friendly')return `${toggle}${caseFieldLinesHTML(c.Preconditions,c.Steps,c.Expected)}`;
  const has=caseHasSimplified(c),stale=has&&caseSimplifiedStale(c);
  if(!has||stale){
    const hint=stale?'用例内容已更改，之前生成的阅读友好版本已过时。':'还没有阅读友好版本 —— 这份用例的步骤/预期结果是给 Planner/生成器看的原始内容，信息密度较高，供人阅读负担较大。';
    return `${toggle}<div class="case-simplify-prompt"><p class="meta">${hint}</p><div class="case-simplify-actions"><button type="button" id="case-simplify-btn">${has?'重新生成阅读友好版本':'生成阅读友好版本'}</button>${has?'<button type="button" class="secondary" id="case-simplify-edit-btn">编辑旧版本</button>':''}</div></div>`;
  }
  return `${toggle}${caseFieldLinesHTML(c.SimplifiedPreconditions,c.SimplifiedSteps,c.SimplifiedExpected)}<p class="meta case-simplify-meta">阅读友好版 · 更新于 ${fmt(c.SimplifiedAt)} <button type="button" class="secondary" id="case-simplify-edit-btn">编辑</button><button type="button" class="secondary" id="case-simplify-btn">重新生成</button></p>`;
}
function bindCaseDetailBody(root,c,isPending,rerender){
  root.querySelectorAll('[data-case-view]').forEach(b=>b.onclick=()=>{caseViewMode=b.dataset.caseView;rerender()});
  const key=simplifyKey(c,isPending),btn=root.querySelector('#case-simplify-btn'),editBtn=root.querySelector('#case-simplify-edit-btn');
  if(btn){btn.dataset.simplifyKey=key;btn.onclick=()=>runSimplify(c,isPending,rerender)}
  if(editBtn){editBtn.dataset.simplifyKey=key;editBtn.onclick=()=>simplifiedEditModal(c,isPending)}
  syncSimplifyButtons(key);
}
// 人工修改阅读友好版：复用 simplifyCase/simplifyPendingCase 持久化（服务端同时把
// SimplifiedFrom* 更新为用例当前内容，所以编辑"已过时"的旧版本保存后即不再过时）。
function simplifiedEditModal(c,isPending){
  if(simplifyInFlight.has(simplifyKey(c,isPending)))return;
  showModal('编辑阅读友好版本',`<label>前置条件<textarea name="SimplifiedPreconditions">${esc(c.SimplifiedPreconditions||'')}</textarea></label><label>执行步骤<textarea name="SimplifiedSteps" required>${esc(c.SimplifiedSteps||'')}</textarea></label><label>预期结果<textarea name="SimplifiedExpected">${esc(c.SimplifiedExpected||'')}</textarea></label>`,x=>{
    const payload={CaseID:c.ID,SimplifiedPreconditions:x.SimplifiedPreconditions,SimplifiedSteps:x.SimplifiedSteps,SimplifiedExpected:x.SimplifiedExpected};
    if(!isPending)payload.VersionID=c.VersionID;
    return (isPending?actReview:act)(isPending?'simplifyPendingCase':'simplifyCase',payload).then(()=>toast('阅读友好版本已保存'));
  });
}
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
      friendly=await plannerRequest('/api/planner/simplify',{method:'POST',headers:{'Content-Type':'application/json'},signal:controller.signal,
        body:JSON.stringify({title:c.Title||'',preconditions:c.Preconditions||'',steps:c.Steps||'',expected:c.Expected||''})});
    }catch(e){toast(controller.signal.aborted?'生成超时，请稍后重试':`生成失败：${e.message}`,true);return}
    clearTimeout(abortTimer);
    const payload={CaseID:c.ID,SimplifiedPreconditions:friendly.preconditions||'',SimplifiedSteps:friendly.steps||'',SimplifiedExpected:friendly.expected||''};
    if(!isPending)payload.VersionID=c.VersionID;
    try{await (isPending?actReview:act)(isPending?'simplifyPendingCase':'simplifyCase',payload);toast('已生成阅读友好版本')}
    catch{}
  }finally{
    clearTimeout(abortTimer);clearInterval(ticker);
    simplifyInFlight.delete(key);
    rerender();
  }
}

function renderFocus(){if(!focus){updateEmptyHint();$('#empty').classList.remove('hidden');$('#detail').classList.add('hidden');return}$('#empty').classList.add('hidden');let d=$('#detail');d.classList.remove('hidden');if(focus.type==='folder'){let f=state.folders.find(x=>x.VersionID===focus.versionID&&x.ID===focus.id);if(!f){focus=null;return renderFocus()}let descendants=descendantFolders(f),count=cases(f.VersionID).filter(c=>c.FolderID===f.ID||descendants.includes(c.FolderID)).length;d.innerHTML=`<div class="detail-head"><div><div class="eyebrow">文件夹 · ${esc(version(f.VersionID).name)}</div><h1>📁 ${esc(f.Name)}</h1></div></div><div class="card meta-grid"><span>用例 <b>${count}</b></span><span>子文件夹 <b>${descendants.length}</b></span><span>创建者 <b>${esc(f.CreatedBy)}</b></span><span>创建时间 <b>${fmt(f.CreatedAt)}</b></span></div>`;return}let c=state.cases.find(x=>x.VersionID===focus.versionID&&x.ID===focus.id);if(!c){focus=null;return renderFocus()}let branch=!version(c.VersionID).mainline,h=state.histories.filter(x=>x.CaseID===c.ID&&x.VersionID===c.VersionID).slice().reverse();d.innerHTML=`<div class="detail-head"><div><div class="eyebrow">${esc(c.ID)} · ${esc(version(c.VersionID).name)} ${c.Dirty?'· 未合并':''}</div><h1>${esc(c.Title)}</h1></div><div class="detail-actions">${branch?'<button id="edit-case" class="secondary">编辑</button>':''}<button id="open-record">测试记录</button></div></div><div class="card case-detail"><div class="meta-grid case-meta"><span>优先级 <b>${esc(c.Priority||'未设置')}</b></span><span>当前结果 <b>${resultName(c.Result)}</b></span><span>更新者 <b>${esc(c.UpdatedBy)}</b></span><span>更新时间 <b>${fmt(c.UpdatedAt)}</b></span><span>基线版本 <b>r${c.BaseRevision||c.Revision}</b></span><button type="button" class="link-button version-toggle" id="case-version-toggle" aria-expanded="false"${h.length?'':' disabled'}>用例版本 <b>${esc(version(c.VersionID).name)}</b><small>${h.length?`${h.length} 条编辑历史 ▾`:'暂无编辑历史'}</small></button></div>${caseDetailBodyHTML(c)}${h.length?`<div class="history-list hidden" id="case-history-list">${h.map(x=>`<a class="history-row" href="#history=${encodeURIComponent(x.ID)}&amp;version=${encodeURIComponent(c.VersionID)}"><b>${historyAction(x.Action)}</b> · ${esc(x.Author)} <small>${fmt(x.CreatedAt)}${x.SourceVersionID?` · 来源 ${esc(version(x.SourceVersionID)?.name||x.SourceVersionID)}`:''}</small></a>`).join('')}</div>`:''}</div>`;if(branch)$('#edit-case').onclick=()=>caseModal(c);$('#open-record').onclick=()=>openRecords(c);if(h.length)$('#case-version-toggle').onclick=()=>{let hidden=$('#case-history-list').classList.toggle('hidden');$('#case-version-toggle').setAttribute('aria-expanded',String(!hidden))};bindCaseDetailBody(d,c,false,renderFocus)}
function descendantFolders(f){let out=[];function walk(id){state.folders.filter(x=>x.VersionID===f.VersionID&&x.ParentID===id).forEach(x=>{out.push(x.ID);walk(x.ID)})}walk(f.ID);return out}
function toggleSelect(v,id,on){if(!selected.has(v))selected.set(v,new Set());on?selected.get(v).add(id):selected.get(v).delete(id);updateBulk();updateFolderChecks()}
function updateBulk(){let entries=[...selected.entries()].filter(([,s])=>s.size);let n=entries.reduce((x,[,s])=>x+s.size,0);$('#bulk').classList.toggle('hidden',!n);$('#selected-count').textContent=`已选 ${n} 项`;}
function folderMenu(v,f){let branch=!version(v).mainline;if(!branch)return f==='root'?[['搜索',()=>openSearch(v,f)],['创建测试版本',versionModal],['复制飞书思维导图',()=>exportFeishuMindmap([{versionID:v,folderID:f}])]]:[['搜索',()=>openSearch(v,f)],['复制飞书思维导图',()=>exportFeishuMindmap([{versionID:v,folderID:f}])]];let items=[['查看详情',()=>setFocus({type:'folder',versionID:v,id:f})],['新建文件夹',()=>folderModal(v,f)],['新增用例',()=>caseModal(null,v,f)],['创建测试任务',()=>taskFromFolder(v,f)],['重命名空文件夹',()=>renameModal(v,f)],['搜索此目录',()=>openSearch(v,f)],['导出目录',()=>exportCases(v,f)],['复制飞书思维导图',()=>exportFeishuMindmap([{versionID:v,folderID:f}])]];if(f!=='root')items.push(['合并到主线',()=>mergeFolderModal(v,f)],['删除空文件夹',()=>{if(confirm('删除该空文件夹？'))act('deleteFolder',{VersionID:v,FolderID:f})}]);return items}
function folderOptionsHTML(versionID){let fs=folders(versionID),roots=fs.filter(f=>!f.ParentID||!fs.some(x=>x.ID===f.ParentID));function node(f,depth){let children=fs.filter(x=>x.ParentID===f.ID);return `<option value="${f.ID}">${'　'.repeat(depth)}${esc(f.Name)}</option>`+children.map(ch=>node(ch,depth+1)).join('')}return roots.map(r=>node(r,0)).join('')}
function targetFolderModal(title,versionID,onSubmit){showModal(title,`<label>目标文件夹<select name="TargetFolderID">${folderOptionsHTML(versionID)}</select></label>`,x=>onSubmit(x.TargetFolderID))}
function mergeFolderModal(v,f){targetFolderModal('合并到主线','main',id=>act('mergeFolder',{VersionID:v,FolderID:f,TargetFolderID:id}))}
function mergeCasesModal(v,ids){targetFolderModal('合并到主线','main',id=>act('mergeCases',{VersionID:v,CaseIDs:ids,TargetFolderID:id}))}
function moveCasesModal(v,ids){targetFolderModal('移动用例',v,id=>act('moveCases',{VersionID:v,CaseIDs:ids,TargetFolderID:id}))}
function caseMenu(v,id){let c=state.cases.find(x=>x.VersionID===v&&x.ID===id),items=[['查看详情',()=>setFocus({type:'case',versionID:v,id})],['测试记录',()=>openRecords(c)]];if(!version(v).mainline)items.push(['编辑用例',()=>caseModal(c)],['删除用例',()=>{if(confirm('确定删除这条用例？测试记录和历史也会一并删除，且无法恢复。'))act('deleteCases',{VersionID:v,CaseIDs:[id]})}]);return items}
function menu(e,items){e.preventDefault();let m=$('#context-menu');m.innerHTML=items.map((x,i)=>`<button data-i="${i}"${x[2]?` class="${x[2]}"`:''}>${esc(x[0])}</button>`).join('');m.style.left=Math.min(e.clientX,innerWidth-205)+'px';m.style.top=Math.min(e.clientY,innerHeight-items.length*38-10)+'px';m.classList.remove('hidden');m.querySelectorAll('button').forEach(b=>b.onclick=()=>{m.classList.add('hidden');items[+b.dataset.i][1]()})}
function showModal(title,html,save){$('#modal-title').textContent=title;$('#modal-body').innerHTML=html;modalSave=save;$('#modal').showModal()}
function versionModal(){showModal('创建测试版本',`<label>版本名称<input name="Name" required placeholder="例如：v2.4.0 回归"></label>`,x=>act('createVersion',x))}
function folderModal(v,parent){showModal('新建文件夹',`<label>文件夹名称<input name="Name" required></label>`,x=>act('createFolder',{...x,VersionID:v,ParentID:parent}))}
function renameModal(v,id){let f=state.folders.find(x=>x.VersionID===v&&x.ID===id);showModal('重命名文件夹',`<label>文件夹名称<input name="Name" required value="${esc(f.Name)}"></label>`,x=>act('renameFolder',{...x,VersionID:v,FolderID:id}))}
function caseModal(c,v,f){v=c?.VersionID||v;f=c?.FolderID||f;showModal(c?'编辑测试用例':'新增测试用例',`<label>标题<input name="Title" required value="${esc(c?.Title||'')}"></label><label>优先级<select name="Priority">${['P0','P1','P2','P3'].map(x=>`<option ${c?.Priority===x?'selected':''}>${x}</option>`).join('')}</select></label><label>前置条件<textarea name="Preconditions">${esc(c?.Preconditions||'')}</textarea></label><label>执行步骤<textarea name="Steps">${esc(c?.Steps||'')}</textarea></label><label>预期结果<textarea name="Expected">${esc(c?.Expected||'')}</textarea></label>`,x=>act(c?'editCase':'createCase',{...x,VersionID:v,FolderID:f,CaseID:c?.ID||''}))}
function taskFromFolder(v,f){let ids=cases(v).filter(c=>c.FolderID===f||descendantFolders(state.folders.find(x=>x.VersionID===v&&x.ID===f)).includes(c.FolderID)).map(c=>c.ID);if(!ids.length)return toast('该目录没有用例',true);taskModal(v,ids)}
function taskModal(v,ids){showModal('创建测试任务',`<label>任务名称<input name="Name" required placeholder="例如：登录模块冒烟测试"></label><p class="meta">包含 ${ids.length} 条用例</p>`,x=>act('createTask',{...x,VersionID:v,CaseIDs:ids}))}
function exportCases(v,f){let subs=f?[f,...descendantFolders(state.folders.find(x=>x.VersionID===v&&x.ID===f))]:folders(v).map(x=>x.ID),data=cases(v).filter(c=>subs.includes(c.FolderID));let a=document.createElement('a');a.href=URL.createObjectURL(new Blob([JSON.stringify(data,null,2)],{type:'application/json'}));a.download=`casehub-${version(v).name}.json`;a.click();URL.revokeObjectURL(a.href)}
function openSearch(v='',f=''){let p=$('#search-panel');p.dataset.version=v;p.dataset.folder=f;p.classList.remove('hidden');$('#workspace').style.gridTemplateColumns=`var(--side) 5px 370px minmax(0,1fr)`;$('#search-query').focus()}
function runSearch(){let q=$('#search-query').value.trim().toLowerCase(),field=$('#search-field').value,result=$('#search-result').value,v=$('#search-panel').dataset.version,f=$('#search-panel').dataset.folder,list=state.cases.filter(c=>(!v||c.VersionID===v));if(f){let fs=[f,...descendantFolders(state.folders.find(x=>x.VersionID===v&&x.ID===f))];list=list.filter(c=>fs.includes(c.FolderID))}list=list.filter(c=>{if(!q)return true;if(field!=='all')return String(c[field]||'').toLowerCase().includes(q);return [c.Title,c.ID,c.Preconditions,c.Steps,c.Expected].some(x=>String(x||'').toLowerCase().includes(q))});if(result)list=list.filter(c=>result==='none'?!c.Result:c.Result===result);let box=$('#search-results');if($('#search-mode').value==='tree'&&v){let ids=new Set(list.map(x=>x.ID));box.innerHTML=tree(v,ids);bindTree(box)}else{box.innerHTML=list.map(c=>{let branch=!version(c.VersionID).mainline,checked=selected.get(c.VersionID)?.has(c.ID);return `<div class="search-hit" data-v="${c.VersionID}" data-c="${c.ID}">${branch?`<input class="case-check" type="checkbox" ${checked?'checked':''}>`:''}<div class="search-hit-body"><b>${esc(c.Title)}</b><small>${esc(c.ID)} · ${esc(version(c.VersionID).name)}</small></div></div>`}).join('')||'<p class="meta">没有匹配结果</p>';box.querySelectorAll('.search-hit').forEach(e=>e.onclick=x=>{if(x.target.matches('input')){toggleSelect(e.dataset.v,e.dataset.c,x.target.checked);return}setFocus({type:'case',versionID:e.dataset.v,id:e.dataset.c})})}}
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
function openRecords(c){recordCase=c;recordHistoryLimit=3;$('#record-history').replaceChildren();$('#record-form').reset();$('#drawer').classList.remove('hidden');resetRecordEditor();let branches=state.versions.filter(v=>!v.mainline&&state.cases.some(x=>x.VersionID===v.id&&x.ID===c.ID));$('#record-version').innerHTML=(version(c.VersionID).mainline?[version(c.VersionID),...branches]:[version(c.VersionID)]).map(v=>`<option value="${v.id}">${esc(v.name)}</option>`).join('');$('#record-version').closest('label').classList.toggle('hidden',!version(c.VersionID).mainline);$('#drawer-case').textContent=`${c.ID} · ${c.Title}`;renderRecordHistory();$('#drawer').classList.remove('hidden')}
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
$$('[data-bulk]').forEach(b=>b.onclick=()=>{if(b.dataset.bulk==='feishu'){exportFeishuMindmap([...selected.entries()].filter(([,ids])=>ids.size).map(([versionID,ids])=>({versionID,ids:[...ids]})));return}let entries=[...selected.entries()].find(([,s])=>s.size);if(!entries)return;let[v,ids]=[entries[0],[...entries[1]]];if(b.dataset.bulk==='export')exportSelected(v,ids);else if(b.dataset.bulk==='task')taskModal(v,ids);else if(b.dataset.bulk==='move')moveCasesModal(v,ids);else if(b.dataset.bulk==='merge')mergeCasesModal(v,ids);else if(b.dataset.bulk==='delete'){if(confirm(`确定删除选中的 ${ids.length} 条用例？测试记录和历史也会一并删除，且无法恢复。`))act('deleteCases',{VersionID:v,CaseIDs:ids}).then(()=>{selected.delete(v);updateBulk()}).catch(()=>{})}});function exportSelected(v,ids){let data=cases(v).filter(c=>ids.includes(c.ID)),a=document.createElement('a');a.href=URL.createObjectURL(new Blob([JSON.stringify(data,null,2)],{type:'application/json'}));a.download='casehub-selected.json';a.click()}
let resizing=false,lastX=340,resizeVar='--side',resizeCollapse='#collapse';
function bindResizer(handleSel,collapseSel,cssVar){$(handleSel).onmousedown=()=>{resizing=true;resizeVar=cssVar;resizeCollapse=collapseSel;$(handleSel).classList.add('dragging')}}
bindResizer('#resize-left','#collapse','--side');
bindResizer('#req-resize-left','#req-collapse','--req-side');
document.onmousemove=e=>{if(resizing){lastX=e.clientX;document.documentElement.style.setProperty(resizeVar,Math.max(0,Math.min(600,e.clientX))+'px')}};
document.onmouseup=()=>{if(resizing&&lastX<70)$(resizeCollapse).click();else if(resizing&&lastX<220)document.documentElement.style.setProperty(resizeVar,'220px');resizing=false;$$('.resizer').forEach(r=>r.classList.remove('dragging'))};
document.documentElement.classList.toggle('dark',localStorage.getItem('casehub-theme')==='dark');
window.addEventListener('hashchange',renderHistoryRoute);
refresh().catch(e=>toast(e.message,true));

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
  const fields=[['Title','标题'],['Priority','优先级'],['Preconditions','前置条件'],['Steps','执行步骤'],['Expected','预期结果']];
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
    d.innerHTML=`<div class="detail-head"><div><div class="eyebrow">待评审文件夹</div><h1>📁 ${esc(f.Name)}</h1></div></div><div class="card meta-grid"><span>用例 <b>${count}</b></span><span>子文件夹 <b>${descendants.length}</b></span><span>创建者 <b>${esc(f.CreatedBy)}</b></span><span>创建时间 <b>${fmt(f.CreatedAt)}</b></span></div>`;
    return;
  }
  if(reqFocus.type==='reviewCase'){
    const c=state.pendingCases.find(x=>x.ID===reqFocus.id);
    if(!c){reqFocus=null;return renderReqFocus()}
    reqEditor?.destroy();reqEditor=null;
    const reviewLabel=({passed:'已通过',rejected:'未通过'})[c.Review]||'待评审';
    d.innerHTML=`<div class="detail-head"><div><div class="eyebrow">${esc(c.ID)} · <span class="badge">${reviewLabel}</span></div><h1>${esc(c.Title)}</h1></div><div class="detail-actions"><button id="edit-review-case" class="secondary">编辑</button><button id="delete-review-case" class="secondary">删除</button><button id="reject-review-case" class="secondary">评审不通过</button><button id="approve-review-case">评审通过</button><button id="import-review-case" class="secondary">导入到版本…</button></div></div><div class="card case-detail"><div class="meta-grid case-meta"><span>优先级 <b>${esc(c.Priority||'未设置')}</b></span><span>评审状态 <b>${reviewLabel}</b></span><span>创建者 <b>${esc(c.CreatedBy)}</b></span><span>更新者 <b>${esc(c.UpdatedBy)}</b></span><span>更新时间 <b>${fmt(c.UpdatedAt)}</b></span>${c.ReviewedBy?`<span>评审人 <b>${esc(c.ReviewedBy)}</b></span><span>评审时间 <b>${fmt(c.ReviewedAt)}</b></span>`:''}</div>${caseDetailBodyHTML(c)}</div>`;
    $('#edit-review-case').onclick=()=>pendingCaseModal(c);
    $('#delete-review-case').onclick=()=>{if(confirm('确定删除这条待评审用例？'))actReview('deletePendingCase',{CaseID:c.ID})};
    $('#approve-review-case').onclick=()=>actReview('reviewPendingCase',{CaseID:c.ID,Review:'passed'});
    $('#reject-review-case').onclick=()=>actReview('reviewPendingCase',{CaseID:c.ID,Review:'rejected'});
    $('#import-review-case').onclick=()=>importReviewModal([c.ID]);
    bindCaseDetailBody(d,c,true,renderReqFocus);
    return;
  }
  const doc=state.reqDocs.find(x=>x.ID===reqFocus.id);
  if(!doc){reqFocus=null;return renderReqFocus()}
  const outRefs=reqRefs(doc.Content).map(id=>state.reqDocs.find(x=>x.ID===id)).filter(Boolean);
  const inRefs=reqBacklinks(doc.ID);
  const relHTML=(outRefs.length||inRefs.length)?`<div class="card req-rel"><h4>关联需求</h4><div class="req-rel-list">${outRefs.map(r=>`<button type="button" class="req-rel-chip" data-req-doc="${esc(r.ID)}">→ ${esc(r.ID)} ${esc(r.Title)}</button>`).join('')}${inRefs.map(r=>`<button type="button" class="req-rel-chip" data-req-doc="${esc(r.ID)}">← ${esc(r.ID)} ${esc(r.Title)}</button>`).join('')}</div></div>`:'';
  d.innerHTML=`<div class="detail-head"><div><div class="eyebrow">${esc(doc.ID)}</div><h1>${esc(doc.Title)}</h1></div><div class="detail-actions"><button type="button" id="req-link-btn" class="secondary">🔗 引用需求</button><button type="button" id="req-ai-design" class="secondary${isAiActive(doc)?' ai-running':''}">${isAiActive(doc)?'AI 设计中':'AI 设计'}</button><button type="button" id="save-req-doc">保存</button></div></div><div class="card meta-grid"><span>创建者 <b>${esc(doc.CreatedBy)}</b></span><span>创建时间 <b>${fmt(doc.CreatedAt)}</b></span><span>更新者 <b>${esc(doc.UpdatedBy)}</b></span><span>更新时间 <b>${fmt(doc.UpdatedAt)}</b></span></div>${relHTML}<div class="card"><div id="req-editor"></div></div>`;
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
async function plannerRequest(path,options){
  const r=await fetch(path,options),ct=r.headers.get('content-type')||'';
  const x=ct.includes('application/json')?await r.json():null;
  if(!r.ok){const e=Error(x?.error?.message||'AI 设计服务请求失败');e.status=r.status;e.code=x?.error?.code;throw e}
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
    try{aiPlannerEnabled=(await plannerRequest('/api/planner/status')).enabled}
    catch{aiPlannerEnabled=false}
  }
  if(aiDoc?.ID!==doc.ID)return; // drawer moved to another doc while awaiting
  if(!aiPlannerEnabled){
    body.innerHTML='<p class="meta">AI 设计服务未配置（缺少 CASEHUB_PLANNER_URL），暂时无法使用。</p>';
    return;
  }
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
    const job=await plannerRequest(`/api/planner/jobs/${jobId}`);
    routeAiJob(doc,job);
  }catch(e){
    localStorage.removeItem(aiJobKey(doc));
    clearAiActive(doc);
    if(isAiDrawerOpen(doc))renderAiForm(doc,'读取任务状态失败，请重新开始。');
  }
}
function renderAiForm(doc,notice){
  // 用例字段特意不叫 name="username"/"password"，且密码框用 type="text" +
  // -webkit-text-security 伪装遮罩：这些只是被测系统的测试账号，不是本机
  // 登录凭据，但字段名/类型撞上 Chrome 的登录表单识别规则后，提交时会触发
  // 它的"密码遭遇数据泄露"弹窗（表单没有真正提交/跳转也会触发，JS 端
  // preventDefault 拦不住）。换掉字段名和输入类型可以让 Chrome 从一开始就不
  // 把这当成登录密码框，从根上避免弹窗，同时视觉上仍然是圆点遮罩。
  $('#ai-drawer-body').innerHTML=`${notice?`<p class="meta">${esc(notice)}</p>`:''}<form id="ai-form" autocomplete="off"><label>被测系统 URL<input name="baseUrl" required placeholder="https://test.example.com/login" autocomplete="off"></label><label>补充说明（可选）<textarea name="instructions" placeholder="覆盖范围、登录方式等"></textarea></label><label>测试账号 · 用户名（可选）<input name="testAccount" autocomplete="off"></label><label>测试账号 · 密码（可选）<input name="testSecret" type="text" class="fake-password" autocomplete="off" spellcheck="false"></label><p class="drawer-actions"><button type="submit">开始设计</button></p></form>`;
  $('#ai-form').onsubmit=async e=>{
    e.preventDefault();
    const x=Object.fromEntries(new FormData(e.target));
    const instructions=[x.instructions||'',reqRefContext(doc)].filter(Boolean).join('\n\n');
    const payload={requirements:[{id:doc.ID,title:doc.Title,content:doc.Content||''}],target:{baseUrl:x.baseUrl},context:{instructions}};
    if(x.testAccount||x.testSecret)payload.context.testData={username:x.testAccount||'',password:x.testSecret||''};
    const btn=e.target.querySelector('button');btn.disabled=true;
    try{
      const job=await plannerRequest('/api/planner/jobs',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(payload)});
      localStorage.setItem(aiJobKey(doc),job.id);
      routeAiJob(doc,job);
    }catch(err){toast(err.message,true);btn.disabled=false}
  };
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
  localStorage.removeItem(aiJobKey(doc));
  if(isAiDrawerOpen(doc))renderAiTerminal(doc,job);
}
function renderAiRunning(doc,job){
  $('#ai-drawer-body').innerHTML=`<div class="card meta-grid"><span>状态 <b>${aiStageLabel(job)}</b></span></div><div id="ai-log" class="ai-log"></div><p class="drawer-actions"><button type="button" class="secondary" id="ai-cancel">取消任务</button></p>`;
  $('#ai-cancel').onclick=async()=>{
    try{await plannerRequest(`/api/planner/jobs/${job.id}`,{method:'DELETE'})}catch(e){toast(e.message,true)}
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
    if(['succeeded','failed','cancelled'].includes(ev.status))plannerRequest(`/api/planner/jobs/${jobId}`).then(job=>routeAiJob(doc,job));
  });
  source.addEventListener('reset',e=>{
    if(!isAiDrawerOpen(doc))return;
    const r=JSON.parse(e.data);
    aiLogLine(`……${r.message||'更早的进度记录已丢失'}`);
  });
}
function renderAiTerminal(doc,job){
  const cancelled=job.status==='cancelled';
  $('#ai-drawer-body').innerHTML=`<div class="card meta-grid"><span>状态 <b>${aiStageLabel(job)}</b></span></div>${job.error?`<p class="meta">${esc(job.error.code)}：${esc(job.error.message)}</p>`:cancelled?'<p class="meta">任务已取消。</p>':''}<p class="drawer-actions"><button type="button" id="ai-retry">重试</button></p>`;
  $('#ai-retry').onclick=()=>renderAiForm(doc);
}
// 设计完成后自动创建"用例评审"目录并导入草稿用例，无需人工点击导入。
async function handleAiSuccess(doc,job){
  if(aiHandlingJobId===job.id)return;
  aiHandlingJobId=job.id;
  if(isAiDrawerOpen(doc))$('#ai-drawer-body').innerHTML='<p class="meta">设计已完成，正在自动导入到"用例评审"…</p>';
  try{
    const result=await plannerRequest(`/api/planner/jobs/${job.id}/result`);
    await importAiResult(doc,result);
    const summary={count:result.cases.length,limitations:result.limitations||[]};
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
function renderAiResult(doc,summary){
  $('#ai-drawer-body').innerHTML=`<div class="card meta-grid"><span>已导入用例 <b>${summary.count}</b></span></div>${summary.limitations?.length?`<div class="card"><h3>未验证/受限范围</h3><ul>${summary.limitations.map(l=>`<li>${esc(l)}</li>`).join('')}</ul></div>`:''}<p class="meta">已自动创建目录并导入到"用例评审"，请前往评审。</p><p class="drawer-actions"><button type="button" class="secondary" id="ai-restart">重新设计</button></p>`;
  $('#ai-restart').onclick=()=>{localStorage.removeItem(aiResultKey(doc));renderAiForm(doc)};
}
async function importAiResult(doc,result){
  const folderName=`${doc.ID} · ${doc.Title}`;
  let folder=state.pendingFolders.find(f=>f.ParentID==='pending-root'&&f.Name===folderName);
  if(!folder){
    const before=new Set(state.pendingFolders.map(f=>f.ID));
    const out=await act('createPendingFolder',{ParentID:'pending-root',Name:folderName});
    folder=out.state.pendingFolders.find(f=>!before.has(f.ID));
  }
  for(const c of result.cases)
    await act('createPendingCase',{FolderID:folder.ID,Title:c.name,Priority:c.priority,Preconditions:c.precondition,Steps:c.steps,Expected:c.expects});
}

// ---- Pending case review (用例评审) ----
function actReview(type,data={}){return act(type,data).then(()=>renderReqFocus())}
const pendingFolder=id=>state.pendingFolders.find(f=>f.ID===id);
const pendingChildren=id=>state.pendingFolders.filter(f=>f.ParentID===id);
const pendingCasesIn=id=>state.pendingCases.filter(c=>c.FolderID===id);
function pendingRoots(){return state.pendingFolders.filter(f=>!f.ParentID||!state.pendingFolders.some(x=>x.ID===f.ParentID))}
function pendingDescendantFolders(f){let out=[];function walk(id){pendingChildren(id).forEach(x=>{out.push(x.ID);walk(x.ID)})}walk(f.ID);return out}
function reviewBadge(c){return c.Review==='passed'?'<span class="result passed" title="评审通过"></span>':c.Review==='rejected'?'<span class="result failed" title="评审不通过"></span>':''}
function reviewCaseRow(c,depth){return `<div class="tree-row case-row" style="padding-left:${8+depth*17}px" data-review-case="${c.ID}"><span class="label">${esc(c.Title)}</span>${reviewBadge(c)}</div>`}
function reviewTreeHTML(){
  function node(f,depth){
    let children=pendingChildren(f.ID),own=pendingCasesIn(f.ID);
    return `<div class="tree-row folder-row" style="padding-left:${8+depth*17}px" data-review-folder="${f.ID}"><span class="chev">▾</span><span>📁</span><span class="label">${esc(f.Name)}</span></div><div>${own.map(c=>reviewCaseRow(c,depth+1)).join('')}${children.map(ch=>node(ch,depth+1)).join('')}</div>`;
  }
  return pendingRoots().map(r=>node(r,0)).join('')||'<p class="meta">暂无待评审用例</p>';
}
function renderReviewTree(){const box=$('#review-tree');box.innerHTML=reviewTreeHTML();bindReviewTree(box)}
function bindReviewTree(root){
  root.querySelectorAll('[data-review-folder]').forEach(e=>{let key=e.dataset.reviewFolder;if(closedReviewFolders.has(key))e.classList.add('closed');e.querySelector('.chev').onclick=x=>{x.stopPropagation();let closed=e.classList.toggle('closed');closed?closedReviewFolders.add(key):closedReviewFolders.delete(key)};e.onclick=()=>setReqFocus({type:'reviewFolder',id:e.dataset.reviewFolder});e.oncontextmenu=x=>menu(x,reviewFolderMenu(e.dataset.reviewFolder))});
  root.querySelectorAll('[data-review-case]').forEach(e=>{e.onclick=()=>setReqFocus({type:'reviewCase',id:e.dataset.reviewCase});e.oncontextmenu=x=>menu(x,reviewCaseMenu(e.dataset.reviewCase))});
}
function pendingCaseIdsIn(folderId){
  const f=pendingFolder(folderId);
  const scope=f?[f.ID,...pendingDescendantFolders(f)]:[folderId];
  return state.pendingCases.filter(c=>scope.includes(c.FolderID)).map(c=>c.ID);
}
function reviewFolderMenu(id){const items=[['查看详情',()=>setReqFocus({type:'reviewFolder',id})],['新建文件夹',()=>pendingFolderModal(id)],['新增用例',()=>pendingCaseModal(null,id)],['导入到版本…',()=>importReviewModal(pendingCaseIdsIn(id))],['重命名空文件夹',()=>renamePendingFolderModal(id)]];if(id!=='pending-root')items.push(['删除空文件夹',()=>{if(confirm('确定删除这个空文件夹？'))actReview('deletePendingFolder',{FolderID:id})}]);return items}
function reviewCaseMenu(id){return [['查看详情',()=>setReqFocus({type:'reviewCase',id})],['编辑',()=>pendingCaseModal(state.pendingCases.find(x=>x.ID===id))],['导入到版本…',()=>importReviewModal([id])],['删除',()=>{if(confirm('确定删除这条待评审用例？'))actReview('deletePendingCase',{CaseID:id})}],['评审通过',()=>actReview('reviewPendingCase',{CaseID:id,Review:'passed'})],['评审不通过',()=>actReview('reviewPendingCase',{CaseID:id,Review:'rejected'})]]}
function pendingFolderModal(parent){showModal('新建文件夹',`<label>文件夹名称<input name="Name" required></label>`,x=>actReview('createPendingFolder',{...x,ParentID:parent}))}
function renamePendingFolderModal(id){const f=pendingFolder(id);showModal('重命名文件夹',`<label>文件夹名称<input name="Name" required value="${esc(f.Name)}"></label>`,x=>actReview('renamePendingFolder',{...x,FolderID:id}))}
function pendingCaseModal(c,folderId){folderId=c?.FolderID||folderId;showModal(c?'编辑待评审用例':'新增待评审用例',`<label>标题<input name="Title" required value="${esc(c?.Title||'')}"></label><label>优先级<select name="Priority">${['P0','P1','P2','P3'].map(x=>`<option ${c?.Priority===x?'selected':''}>${x}</option>`).join('')}</select></label><label>前置条件<textarea name="Preconditions">${esc(c?.Preconditions||'')}</textarea></label><label>执行步骤<textarea name="Steps">${esc(c?.Steps||'')}</textarea></label><label>预期结果<textarea name="Expected">${esc(c?.Expected||'')}</textarea></label>`,x=>actReview(c?'editPendingCase':'createPendingCase',{...x,FolderID:folderId,CaseID:c?.ID||''}))}
function importReviewModal(caseIds){
  if(!caseIds.length)return toast('该范围内没有待评审用例',true);
  const unreviewed=state.pendingCases.filter(c=>caseIds.includes(c.ID)&&c.Review!=='passed');
  if(unreviewed.length)return toast(`还有 ${unreviewed.length} 条用例未通过评审，无法导入`,true);
  const branches=state.versions.filter(v=>!v.mainline);
  if(!branches.length)return toast('请先创建一个测试版本作为导入目标',true);
  showModal('导入到版本',`<label>目标版本<select name="VersionID">${branches.map(v=>`<option value="${v.id}">${esc(v.name)}</option>`).join('')}</select></label><p class="meta">将导入 ${caseIds.length} 条已评审通过的用例，并保持目录结构。</p>`,x=>act('importPendingCases',{...x,CaseIDs:caseIds}).then(()=>{reqFocus=null;renderReqFocus();toast('已导入到目标版本')}))
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
  const isReq=p==='requirements';
  $('#app-subtitle').textContent=isReq?'需求文档管理':'测试用例管理';
  $('#workspace').classList.toggle('hidden',isReq);
  $('#req-workspace').classList.toggle('hidden',!isReq);
  if(isReq)updateReqEmptyHint();
}
$$('.page-tab').forEach(b=>b.onclick=()=>setPage(b.dataset.app));
