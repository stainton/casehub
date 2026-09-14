const $=s=>document.querySelector(s), $$=s=>[...document.querySelectorAll(s)];
let state=null, focus=null, selected=new Map(), view='cases', modalSave=null, recordCase=null, recordTask='', recordEditor=null, recordViewers=[], recordHistoryLimit=3;
let openTasks=new Set(), closedTaskFolders=new Set(), closedFolders=new Set(), closedReqFolders=new Set(), closedReviewFolders=new Set();
let page='cases', reqFocus=null, reqEditor=null, reqSidebarWidth=null, aiDoc=null, reqPage='docs';
let aiSource=null, aiPlannerEnabled=null;
const esc=s=>String(s??'').replace(/[&<>'"]/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;',"'":'&#39;','"':'&quot;'}[c]));
const fmt=s=>s?new Date(s).toLocaleString():'—';
const version=id=>state.versions.find(v=>v.id===id), folders=id=>state.folders.filter(f=>f.VersionID===id), cases=id=>state.cases.filter(c=>c.VersionID===id);
async function request(path,options){let r=await fetch(path,options),x=await r.json();if(!r.ok){let e=Error(x.error||'请求失败');e.status=r.status;e.conflicts=x.conflicts;throw e}return x}
function normalizeState(s){s=s||{};for(const key of ['versions','folders','cases','histories','records','tasks','reqFolders','reqDocs','pendingFolders','pendingCases'])if(!Array.isArray(s[key]))s[key]=[];return s}
async function refresh(){state=normalizeState(await request('/api/state'));render();}
async function act(type,data={},retry=false){try{let out=await request('/api/action',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({Type:type,Author:'本地用户',...data})});state=normalizeState(out.state);if(out.warnings?.length)toast(out.warnings.join('；'));render();return out}catch(e){if(e.status===409&&type.startsWith('merge')){alert(`${e.message}\n冲突用例：${(e.conflicts||[]).join(', ')}\n请拉取主线，然后打开冲突用例编辑并确认人工处理。`)}else if(e.status===409&&!retry&&confirm(`${e.message}\n冲突用例：${(e.conflicts||[]).join(', ')}\n是否以当前编辑内容作为人工解决结果？`))return act(type,{...data,Force:true},true);toast(e.message,true);throw e}}
function render(){ renderVersions();renderTasks();renderFocus();if(recordTask)$('#edit-case')?.remove();updateBulk();if(location.hash)renderHistoryRoute();renderReqTree();renderReviewTree(); }
function renderVersions(){let box=$('#versions');box.innerHTML=state.versions.map(v=>`<div class="version" data-version="${v.id}"><div class="version-title"><span class="chev">⌄</span><span>${esc(v.name)}</span><span class="badge">${v.mainline?'只读主线':'测试版本'}</span>${v.mainline?'':`<span class="version-actions"><button data-sync="${v.id}">拉取主线</button><button data-merge="${v.id}">合并</button></span>`}</div><div class="version-body">${tree(v.id)}</div></div>`).join('');bindTree(box);}
function tree(vid,onlyIDs=null,taskID=''){let fs=folders(vid),cs=cases(vid),roots=fs.filter(f=>!f.ParentID||!fs.some(x=>x.ID===f.ParentID));let branch=!version(vid).mainline,showCheck=branch&&!taskID;function node(f,depth){let children=fs.filter(x=>x.ParentID===f.ID),own=cs.filter(c=>c.FolderID===f.ID);let visible=!onlyIDs||own.some(c=>onlyIDs.has(c.ID))||children.some(ch=>hasHit(ch));if(!visible)return'';return `<div class="tree-row folder-row" style="padding-left:${8+depth*17}px" data-folder="${f.ID}" data-version="${vid}">${showCheck?'<input class="folder-check" type="checkbox">':''}<span class="chev">▾</span><span>📁</span><span class="label">${esc(f.Name)}</span></div><div>${own.filter(c=>!onlyIDs||onlyIDs.has(c.ID)).map(c=>caseRow(c,depth+1,branch&&!taskID,taskID)).join('')}${children.map(ch=>node(ch,depth+1)).join('')}</div>`}function hasHit(f){return cs.some(c=>c.FolderID===f.ID&&onlyIDs.has(c.ID))||fs.filter(x=>x.ParentID===f.ID).some(hasHit)}return roots.map(r=>node(r,0)).join('')||'<p class="meta">空版本</p>'}
function caseRow(c,depth,branch,taskID=''){let checked=selected.get(c.VersionID)?.has(c.ID);return `<div class="tree-row ${taskID?'task-case':'case-row'}" style="padding-left:${8+depth*17}px" data-case="${c.ID}" data-c="${c.ID}" data-version="${c.VersionID}" data-v="${c.VersionID}" ${taskID?`data-task="${taskID}"`:''}>${branch?`<input class="case-check" type="checkbox" ${checked?'checked':''}>`:''}<span class="label">${esc(c.Title)}</span>${c.Result?`<span class="result ${c.Result}"></span>`:''}</div>`}
function bindTree(root){root.querySelectorAll('.version-title').forEach(e=>e.onclick=x=>{if(x.target.closest('button'))return;e.parentElement.classList.toggle('closed')});root.querySelectorAll('.folder-row').forEach(e=>{let key=`${e.dataset.version}:${e.dataset.folder}`;if(closedFolders.has(key))e.classList.add('closed');e.querySelector('.chev').onclick=x=>{x.stopPropagation();let closed=e.classList.toggle('closed');closed?closedFolders.add(key):closedFolders.delete(key)};let fc=e.querySelector('.folder-check');if(fc)fc.onclick=x=>{x.stopPropagation();toggleFolderSelect(e.dataset.version,e.dataset.folder,fc.checked)};e.onclick=x=>{if(x.target.matches('input'))return;setFocus({type:'folder',versionID:e.dataset.version,id:e.dataset.folder})};e.oncontextmenu=x=>menu(x,folderMenu(e.dataset.version,e.dataset.folder))});root.querySelectorAll('.case-row').forEach(e=>{e.onclick=x=>{if(x.target.matches('input')){toggleSelect(e.dataset.version,e.dataset.case,x.target.checked);return}setFocus({type:'case',versionID:e.dataset.version,id:e.dataset.case})};e.oncontextmenu=x=>menu(x,caseMenu(e.dataset.version,e.dataset.case))});root.querySelectorAll('[data-sync]').forEach(e=>e.onclick=()=>act('sync',{VersionID:e.dataset.sync}));root.querySelectorAll('[data-merge]').forEach(e=>e.onclick=()=>{if(confirm('将此版本中的文本变更与新增目录合并到只读主线？'))act('merge',{VersionID:e.dataset.merge})});updateFolderChecks(root)}
function updateFolderChecks(root=document){root.querySelectorAll('.folder-row').forEach(e=>{let fc=e.querySelector('.folder-check');if(!fc)return;let f=state.folders.find(x=>x.VersionID===e.dataset.version&&x.ID===e.dataset.folder);if(!f)return;let subIDs=[f.ID,...descendantFolders(f)],all=cases(e.dataset.version).filter(c=>subIDs.includes(c.FolderID)),selSet=selected.get(e.dataset.version),selCount=all.filter(c=>selSet?.has(c.ID)).length;fc.checked=all.length>0&&selCount===all.length;fc.indeterminate=selCount>0&&selCount<all.length})}
function toggleFolderSelect(vid,folderId,checked){let f=state.folders.find(x=>x.VersionID===vid&&x.ID===folderId);if(!f)return;let subIDs=[f.ID,...descendantFolders(f)];if(!selected.has(vid))selected.set(vid,new Set());let s=selected.get(vid);cases(vid).filter(c=>subIDs.includes(c.FolderID)).forEach(c=>checked?s.add(c.ID):s.delete(c.ID));updateBulk();renderVersions()}
function setFocus(x){if(!$('#drawer').classList.contains('hidden')&&!(x.type==='case'&&recordCase?.ID===x.id))closeRecordDrawer();if(location.hash)history.replaceState(null,'',location.pathname+location.search);focus=x;recordTask=x.taskID||'';renderVersions();renderFocus()}
function closeRecordDrawer(){$('#record-form').reset();recordEditor?.setMarkdown('');$('#drawer').classList.add('hidden')}
function renderFocus(){if(!focus){updateEmptyHint();$('#empty').classList.remove('hidden');$('#detail').classList.add('hidden');return}$('#empty').classList.add('hidden');let d=$('#detail');d.classList.remove('hidden');if(focus.type==='folder'){let f=state.folders.find(x=>x.VersionID===focus.versionID&&x.ID===focus.id);if(!f){focus=null;return renderFocus()}let descendants=descendantFolders(f),count=cases(f.VersionID).filter(c=>c.FolderID===f.ID||descendants.includes(c.FolderID)).length;d.innerHTML=`<div class="detail-head"><div><div class="eyebrow">文件夹 · ${esc(version(f.VersionID).name)}</div><h1>📁 ${esc(f.Name)}</h1></div></div><div class="card meta-grid"><span>用例 <b>${count}</b></span><span>子文件夹 <b>${descendants.length}</b></span><span>创建者 <b>${esc(f.CreatedBy)}</b></span><span>创建时间 <b>${fmt(f.CreatedAt)}</b></span></div>`;return}let c=state.cases.find(x=>x.VersionID===focus.versionID&&x.ID===focus.id);if(!c){focus=null;return renderFocus()}let branch=!version(c.VersionID).mainline,h=state.histories.filter(x=>x.CaseID===c.ID&&(x.VersionID===c.VersionID||c.VersionID==='main')).slice().reverse();d.innerHTML=`<div class="detail-head"><div><div class="eyebrow">${esc(c.ID)} · ${esc(version(c.VersionID).name)} ${c.Dirty?'· 未合并':''}</div><h1>${esc(c.Title)}</h1></div><div class="detail-actions">${branch?'<button id="edit-case" class="secondary">编辑</button>':''}<button id="open-record">测试记录</button></div></div><div class="card case-detail"><div class="meta-grid case-meta"><span>优先级 <b>${esc(c.Priority||'未设置')}</b></span><span>当前结果 <b>${resultName(c.Result)}</b></span><span>更新者 <b>${esc(c.UpdatedBy)}</b></span><span>更新时间 <b>${fmt(c.UpdatedAt)}</b></span><span>基线版本 <b>r${c.BaseRevision||c.Revision}</b></span><button type="button" class="link-button version-toggle" id="case-version-toggle" aria-expanded="false"${h.length?'':' disabled'}>用例版本 <b>${esc(version(c.VersionID).name)}</b><small>${h.length?`${h.length} 条编辑历史 ▾`:'暂无编辑历史'}</small></button></div><div class="case-lines"><div class="field-line"><h4>前置条件</h4><p>${esc(c.Preconditions||'—')}</p></div><div class="field-line"><h4>执行步骤</h4><p>${esc(c.Steps||'—')}</p></div><div class="field-line"><h4>预期结果</h4><p>${esc(c.Expected||'—')}</p></div></div>${h.length?`<div class="history-list hidden" id="case-history-list">${h.map(x=>`<a class="history-row" href="#history=${encodeURIComponent(x.ID)}&amp;version=${encodeURIComponent(c.VersionID)}"><b>${historyAction(x.Action)}</b> · ${esc(x.Author)} <small>${fmt(x.CreatedAt)}${x.SourceVersionID?` · 来源 ${esc(version(x.SourceVersionID)?.name||x.SourceVersionID)}`:''}</small></a>`).join('')}</div>`:''}</div>`;if(branch)$('#edit-case').onclick=()=>caseModal(c);$('#open-record').onclick=()=>openRecords(c);if(h.length)$('#case-version-toggle').onclick=()=>{let hidden=$('#case-history-list').classList.toggle('hidden');$('#case-version-toggle').setAttribute('aria-expanded',String(!hidden))}}
function descendantFolders(f){let out=[];function walk(id){state.folders.filter(x=>x.VersionID===f.VersionID&&x.ParentID===id).forEach(x=>{out.push(x.ID);walk(x.ID)})}walk(f.ID);return out}
function toggleSelect(v,id,on){if(!selected.has(v))selected.set(v,new Set());on?selected.get(v).add(id):selected.get(v).delete(id);updateBulk();updateFolderChecks()}
function updateBulk(){let entries=[...selected.entries()].filter(([,s])=>s.size);let n=entries.reduce((x,[,s])=>x+s.size,0);$('#bulk').classList.toggle('hidden',!n);$('#selected-count').textContent=`已选 ${n} 项`;}
function folderMenu(v,f){let branch=!version(v).mainline;if(!branch)return f==='root'?[['搜索',()=>openSearch(v,f)],['创建测试版本',versionModal]]:[['搜索',()=>openSearch(v,f)]];let items=[['查看详情',()=>setFocus({type:'folder',versionID:v,id:f})],['新建文件夹',()=>folderModal(v,f)],['新增用例',()=>caseModal(null,v,f)],['创建测试任务',()=>taskFromFolder(v,f)],['重命名空文件夹',()=>renameModal(v,f)],['搜索此目录',()=>openSearch(v,f)],['导出目录',()=>exportCases(v,f)]];if(f!=='root')items.push(['合并到主线',()=>mergeFolderModal(v,f)],['删除空文件夹',()=>{if(confirm('删除该空文件夹？'))act('deleteFolder',{VersionID:v,FolderID:f})}]);return items}
function folderOptionsHTML(versionID){let fs=folders(versionID),roots=fs.filter(f=>!f.ParentID||!fs.some(x=>x.ID===f.ParentID));function node(f,depth){let children=fs.filter(x=>x.ParentID===f.ID);return `<option value="${f.ID}">${'　'.repeat(depth)}${esc(f.Name)}</option>`+children.map(ch=>node(ch,depth+1)).join('')}return roots.map(r=>node(r,0)).join('')}
function targetFolderModal(title,versionID,onSubmit){showModal(title,`<label>目标文件夹<select name="TargetFolderID">${folderOptionsHTML(versionID)}</select></label>`,x=>onSubmit(x.TargetFolderID))}
function mergeFolderModal(v,f){targetFolderModal('合并到主线','main',id=>act('mergeFolder',{VersionID:v,FolderID:f,TargetFolderID:id}))}
function mergeCasesModal(v,ids){targetFolderModal('合并到主线','main',id=>act('mergeCases',{VersionID:v,CaseIDs:ids,TargetFolderID:id}))}
function moveCasesModal(v,ids){targetFolderModal('移动用例',v,id=>act('moveCases',{VersionID:v,CaseIDs:ids,TargetFolderID:id}))}
function caseMenu(v,id){let c=state.cases.find(x=>x.VersionID===v&&x.ID===id),items=[['查看详情',()=>setFocus({type:'case',versionID:v,id})],['测试记录',()=>openRecords(c)]];if(!version(v).mainline)items.push(['编辑用例',()=>caseModal(c)]);return items}
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
function renderTasks(){let box=$('#tasks');box.innerHTML=state.tasks.map(t=>`<div class="task" data-task="${t.ID}"><div class="task-head" data-task="${t.ID}">▸ ${esc(t.Name)} <small>(${t.CaseIDs.length})</small></div><div class="task-body${openTasks.has(t.ID)?'':' hidden'}">${tree(t.VersionID,new Set(t.CaseIDs),t.ID)}</div></div>`).join('')||'<p class="meta" style="padding:15px">暂无测试任务</p>';box.querySelectorAll('.task-head').forEach(e=>e.onclick=()=>{let id=e.dataset.task,hidden=e.nextElementSibling.classList.toggle('hidden');hidden?openTasks.delete(id):openTasks.add(id)});box.querySelectorAll('.task').forEach(taskEl=>{let taskID=taskEl.dataset.task;taskEl.querySelectorAll('.folder-row').forEach(e=>{let key=`${taskID}:${e.dataset.folder}`;if(closedTaskFolders.has(key))e.classList.add('closed');e.querySelector('.chev').onclick=x=>{x.stopPropagation();let closed=e.classList.toggle('closed');closed?closedTaskFolders.add(key):closedTaskFolders.delete(key)}})});box.querySelectorAll('.task-case').forEach(e=>{e.onclick=()=>setFocus({type:'case',versionID:e.dataset.v,id:e.dataset.c,taskID:e.dataset.task});e.oncontextmenu=x=>menu(x,taskCaseMenu(e.dataset.v,e.dataset.c,e.dataset.task))})}
function taskCaseMenu(v,id,taskID){let c=state.cases.find(x=>x.VersionID===v&&x.ID===id);return [['查看详情',()=>setFocus({type:'case',versionID:v,id,taskID})],['测试记录',()=>openRecords(c)],['标记通过',()=>markResult(v,id,taskID,'passed')],['标记失败',()=>markResult(v,id,taskID,'failed')],['标记阻塞',()=>markResult(v,id,taskID,'blocked')]]}
function markResult(v,id,taskID,result){return act('submitRecord',{Result:result,Note:'',VersionID:v,CaseID:id,TaskID:taskID}).then(()=>toast(`已标记为${resultName(result)}`))}
function openRecords(c){recordCase=c;recordHistoryLimit=3;$('#record-history').replaceChildren();$('#record-form').reset();$('#drawer').classList.remove('hidden');resetRecordEditor();let branches=state.versions.filter(v=>!v.mainline&&state.cases.some(x=>x.VersionID===v.id&&x.ID===c.ID));$('#record-version').innerHTML=(version(c.VersionID).mainline?branches:[version(c.VersionID)]).map(v=>`<option value="${v.id}">${esc(v.name)}</option>`).join('');$('#record-version').closest('label').classList.toggle('hidden',!version(c.VersionID).mainline);$('#drawer-case').textContent=`${c.ID} · ${c.Title}`;renderRecordHistory();$('#drawer').classList.remove('hidden')}
function renderRecordHistory(){
  const box=$('#record-history'),opened=new Set([...box.querySelectorAll('.record[open]')].map(el=>el.dataset.id));
  recordViewers.forEach(v=>v.destroy());recordViewers=[];
  const vid=$('#record-version').value||recordCase.VersionID;
  const records=state.records.filter(r=>r.VersionID===vid&&r.CaseID===recordCase.ID).slice().reverse();
  if(!records.length){box.innerHTML='<p class="meta">暂无测试记录</p>';return}
  box.innerHTML=`<div class="record-history-heading"><strong>历史记录</strong><small>共 ${records.length} 条</small></div>`+records.slice(0,recordHistoryLimit).map(r=>{
    const preview=(r.Note||'无备注').replace(/\s+/g,' ').trim();
    return `<details class="record" data-id="${esc(r.ID)}" ${opened.has(r.ID)?'open':''}><summary><span class="record-summary-title"><b>${resultName(r.Result)}</b> · ${r.Submitted?'已提交':'草稿'}</span><small>${esc(r.Author)} · ${fmt(r.CreatedAt)}</small><span class="record-preview">${esc(preview.slice(0,80))}${preview.length>80?'…':''}</span></summary><div class="record-note"></div></details>`;
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
$$('[data-bulk]').forEach(b=>b.onclick=()=>{let entries=[...selected.entries()].find(([,s])=>s.size);if(!entries)return;let[v,ids]=[entries[0],[...entries[1]]];if(b.dataset.bulk==='export')exportSelected(v,ids);else if(b.dataset.bulk==='task')taskModal(v,ids);else if(b.dataset.bulk==='move')moveCasesModal(v,ids);else if(b.dataset.bulk==='merge')mergeCasesModal(v,ids)});function exportSelected(v,ids){let data=cases(v).filter(c=>ids.includes(c.ID)),a=document.createElement('a');a.href=URL.createObjectURL(new Blob([JSON.stringify(data,null,2)],{type:'application/json'}));a.download='casehub-selected.json';a.click()}
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
    d.innerHTML=`<div class="detail-head"><div><div class="eyebrow">${esc(c.ID)} · <span class="badge">${reviewLabel}</span></div><h1>${esc(c.Title)}</h1></div><div class="detail-actions"><button id="edit-review-case" class="secondary">编辑</button><button id="delete-review-case" class="secondary">删除</button><button id="reject-review-case" class="secondary">评审不通过</button><button id="approve-review-case">评审通过</button><button id="import-review-case" class="secondary">导入到版本…</button></div></div><div class="card case-detail"><div class="meta-grid case-meta"><span>优先级 <b>${esc(c.Priority||'未设置')}</b></span><span>评审状态 <b>${reviewLabel}</b></span><span>创建者 <b>${esc(c.CreatedBy)}</b></span><span>更新者 <b>${esc(c.UpdatedBy)}</b></span><span>更新时间 <b>${fmt(c.UpdatedAt)}</b></span>${c.ReviewedBy?`<span>评审人 <b>${esc(c.ReviewedBy)}</b></span><span>评审时间 <b>${fmt(c.ReviewedAt)}</b></span>`:''}</div><div class="case-lines"><div class="field-line"><h4>前置条件</h4><p>${esc(c.Preconditions||'—')}</p></div><div class="field-line"><h4>执行步骤</h4><p>${esc(c.Steps||'—')}</p></div><div class="field-line"><h4>预期结果</h4><p>${esc(c.Expected||'—')}</p></div></div></div>`;
    $('#edit-review-case').onclick=()=>pendingCaseModal(c);
    $('#delete-review-case').onclick=()=>{if(confirm('确定删除这条待评审用例？'))actReview('deletePendingCase',{CaseID:c.ID})};
    $('#approve-review-case').onclick=()=>actReview('reviewPendingCase',{CaseID:c.ID,Review:'passed'});
    $('#reject-review-case').onclick=()=>actReview('reviewPendingCase',{CaseID:c.ID,Review:'rejected'});
    $('#import-review-case').onclick=()=>importReviewModal([c.ID]);
    return;
  }
  const doc=state.reqDocs.find(x=>x.ID===reqFocus.id);
  if(!doc){reqFocus=null;return renderReqFocus()}
  d.innerHTML=`<div class="detail-head"><div><div class="eyebrow">${esc(doc.ID)}</div><h1>${esc(doc.Title)}</h1></div><div class="detail-actions"><button type="button" id="req-ai-design" class="secondary${isAiActive(doc)?' ai-running':''}">${isAiActive(doc)?'AI 设计中':'AI 设计'}</button><button type="button" id="save-req-doc">保存</button></div></div><div class="card meta-grid"><span>创建者 <b>${esc(doc.CreatedBy)}</b></span><span>创建时间 <b>${fmt(doc.CreatedAt)}</b></span><span>更新者 <b>${esc(doc.UpdatedBy)}</b></span><span>更新时间 <b>${fmt(doc.UpdatedAt)}</b></span></div><div class="card"><div id="req-editor"></div></div>`;
  reqEditor?.destroy();
  reqEditor=new toastui.Editor({el:$('#req-editor'),height:'520px',initialEditType:'wysiwyg',previewStyle:'tab',initialValue:doc.Content||'',language:'zh-CN',theme:document.documentElement.classList.contains('dark')?'dark':'light',usageStatistics:false});
  $('#save-req-doc').onclick=()=>saveReqDoc(doc).catch(()=>{});
  $('#req-ai-design').onclick=()=>openAiDrawer(doc);
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
    if(isAiDrawerOpen(doc))renderAiForm(doc);
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
  $('#ai-drawer-body').innerHTML=`${notice?`<p class="meta">${esc(notice)}</p>`:''}<form id="ai-form"><label>被测系统 URL<input name="baseUrl" required placeholder="https://test.example.com/login"></label><label>补充说明（可选）<textarea name="instructions" placeholder="覆盖范围、登录方式等"></textarea></label><label>测试账号 · 用户名（可选）<input name="username"></label><label>测试账号 · 密码（可选）<input name="password" type="password"></label><p class="drawer-actions"><button type="submit">开始设计</button></p></form>`;
  $('#ai-form').onsubmit=async e=>{
    e.preventDefault();
    const x=Object.fromEntries(new FormData(e.target));
    const payload={requirements:[{id:doc.ID,title:doc.Title,content:doc.Content||''}],target:{baseUrl:x.baseUrl},context:{instructions:x.instructions||''}};
    if(x.username||x.password)payload.context.testData={username:x.username||'',password:x.password||''};
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
    localStorage.removeItem(aiJobKey(doc));
    toast(`AI 设计已完成，已自动导入 ${result.cases.length} 条用例到"用例评审"`);
    if(isAiDrawerOpen(doc)){
      $('#ai-drawer-body').innerHTML=`<div class="card meta-grid"><span>已导入用例 <b>${result.cases.length}</b></span></div>${result.limitations?.length?`<div class="card"><h3>未验证/受限范围</h3><ul>${result.limitations.map(l=>`<li>${esc(l)}</li>`).join('')}</ul></div>`:''}<p class="meta">已自动创建目录并导入到"用例评审"，请前往评审。</p><p class="drawer-actions"><button type="button" class="secondary" id="ai-restart">重新设计</button></p>`;
      $('#ai-restart').onclick=()=>renderAiForm(doc);
    }
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
