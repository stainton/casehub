const $=s=>document.querySelector(s), $$=s=>[...document.querySelectorAll(s)];
let state=null, focus=null, selected=new Map(), view='cases', modalSave=null, recordCase=null, recordTask='', recordEditor=null, recordViewers=[], recordHistoryLimit=3;
let page='cases', reqFocus=null, reqEditor=null, reqSidebarWidth=null, aiDoc=null;
const esc=s=>String(s??'').replace(/[&<>'"]/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;',"'":'&#39;','"':'&quot;'}[c]));
const fmt=s=>s?new Date(s).toLocaleString():'—';
const version=id=>state.versions.find(v=>v.id===id), folders=id=>state.folders.filter(f=>f.VersionID===id), cases=id=>state.cases.filter(c=>c.VersionID===id);
async function request(path,options){let r=await fetch(path,options),x=await r.json();if(!r.ok){let e=Error(x.error||'请求失败');e.status=r.status;e.conflicts=x.conflicts;throw e}return x}
function normalizeState(s){s=s||{};for(const key of ['versions','folders','cases','histories','records','tasks','reqFolders','reqDocs'])if(!Array.isArray(s[key]))s[key]=[];return s}
async function refresh(){state=normalizeState(await request('/api/state'));render();}
async function act(type,data={},retry=false){try{let out=await request('/api/action',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({Type:type,Author:'本地用户',...data})});state=normalizeState(out.state);if(out.warnings?.length)toast(out.warnings.join('；'));render();return out}catch(e){if(e.status===409&&type==='merge'){alert(`${e.message}\n冲突用例：${(e.conflicts||[]).join(', ')}\n请拉取主线，然后打开冲突用例编辑并确认人工处理。`)}else if(e.status===409&&!retry&&confirm(`${e.message}\n冲突用例：${(e.conflicts||[]).join(', ')}\n是否以当前编辑内容作为人工解决结果？`))return act(type,{...data,Force:true},true);toast(e.message,true);throw e}}
function render(){ $('#revision').textContent=`主线 r${state.mainRevision}`;renderVersions();renderTasks();renderFocus();if(recordTask)$('#edit-case')?.remove();updateBulk();if(location.hash)renderHistoryRoute();renderReqTree(); }
function renderVersions(){let box=$('#versions');box.innerHTML=state.versions.map(v=>`<div class="version" data-version="${v.id}"><div class="version-title"><span class="chev">⌄</span><span>${esc(v.name)}</span><span class="badge">${v.mainline?'只读主线':'测试版本'}</span>${v.mainline?'':`<span class="version-actions"><button data-sync="${v.id}">拉取主线</button><button data-merge="${v.id}">合并</button></span>`}</div><div class="version-body">${tree(v.id)}</div></div>`).join('');bindTree(box);}
function tree(vid,onlyIDs=null,taskID=''){let fs=folders(vid),cs=cases(vid),roots=fs.filter(f=>!f.ParentID||!fs.some(x=>x.ID===f.ParentID));let branch=!version(vid).mainline;function node(f,depth){let children=fs.filter(x=>x.ParentID===f.ID),own=cs.filter(c=>c.FolderID===f.ID);let visible=!onlyIDs||own.some(c=>onlyIDs.has(c.ID))||children.some(ch=>hasHit(ch));if(!visible)return'';return `<div class="tree-row folder-row" style="padding-left:${8+depth*17}px" data-folder="${f.ID}" data-version="${vid}"><span>▾</span><span>📁</span><span class="label">${esc(f.Name)}</span></div><div>${own.filter(c=>!onlyIDs||onlyIDs.has(c.ID)).map(c=>caseRow(c,depth+1,branch&&!taskID,taskID)).join('')}${children.map(ch=>node(ch,depth+1)).join('')}</div>`}function hasHit(f){return cs.some(c=>c.FolderID===f.ID&&onlyIDs.has(c.ID))||fs.filter(x=>x.ParentID===f.ID).some(hasHit)}return roots.map(r=>node(r,0)).join('')||'<p class="meta">空版本</p>'}
function caseRow(c,depth,branch,taskID=''){let checked=selected.get(c.VersionID)?.has(c.ID);return `<div class="tree-row ${taskID?'task-case':'case-row'}" style="padding-left:${8+depth*17}px" data-case="${c.ID}" data-c="${c.ID}" data-version="${c.VersionID}" data-v="${c.VersionID}" ${taskID?`data-task="${taskID}"`:''}>${branch?`<input class="case-check" type="checkbox" ${checked?'checked':''}>`:''}<span class="label">${esc(c.Title)}</span>${c.Dirty?'<span title="未合并">●</span>':''}${c.Result?`<span class="result ${c.Result}"></span>`:''}</div>`}
function bindTree(root){root.querySelectorAll('.version-title').forEach(e=>e.onclick=x=>{if(x.target.closest('button'))return;e.parentElement.classList.toggle('closed')});root.querySelectorAll('.folder-row').forEach(e=>{e.onclick=()=>setFocus({type:'folder',versionID:e.dataset.version,id:e.dataset.folder});e.oncontextmenu=x=>menu(x,folderMenu(e.dataset.version,e.dataset.folder))});root.querySelectorAll('.case-row').forEach(e=>{e.onclick=x=>{if(x.target.matches('input')){toggleSelect(e.dataset.version,e.dataset.case,x.target.checked);return}setFocus({type:'case',versionID:e.dataset.version,id:e.dataset.case})};e.oncontextmenu=x=>menu(x,caseMenu(e.dataset.version,e.dataset.case))});root.querySelectorAll('[data-sync]').forEach(e=>e.onclick=()=>act('sync',{VersionID:e.dataset.sync}));root.querySelectorAll('[data-merge]').forEach(e=>e.onclick=()=>{if(confirm('将此版本中的文本变更与新增目录合并到只读主线？'))act('merge',{VersionID:e.dataset.merge})})}
function setFocus(x){if(location.hash)history.replaceState(null,'',location.pathname+location.search);focus=x;recordTask=x.taskID||'';renderVersions();renderFocus()}
function renderFocus(){if(!focus){updateEmptyHint();$('#empty').classList.remove('hidden');$('#detail').classList.add('hidden');return}$('#empty').classList.add('hidden');let d=$('#detail');d.classList.remove('hidden');if(focus.type==='folder'){let f=state.folders.find(x=>x.VersionID===focus.versionID&&x.ID===focus.id);if(!f){focus=null;return renderFocus()}let descendants=descendantFolders(f),count=cases(f.VersionID).filter(c=>c.FolderID===f.ID||descendants.includes(c.FolderID)).length;d.innerHTML=`<div class="detail-head"><div><div class="eyebrow">文件夹 · ${esc(version(f.VersionID).name)}</div><h1>📁 ${esc(f.Name)}</h1></div></div><div class="card meta-grid"><span>用例 <b>${count}</b></span><span>子文件夹 <b>${descendants.length}</b></span><span>创建者 <b>${esc(f.CreatedBy)}</b></span><span>创建时间 <b>${fmt(f.CreatedAt)}</b></span></div>`;return}let c=state.cases.find(x=>x.VersionID===focus.versionID&&x.ID===focus.id);if(!c){focus=null;return renderFocus()}let branch=!version(c.VersionID).mainline,h=state.histories.filter(x=>x.CaseID===c.ID&&(x.VersionID===c.VersionID||c.VersionID==='main')).slice().reverse();d.innerHTML=`<div class="detail-head"><div><div class="eyebrow">${esc(c.ID)} · ${esc(version(c.VersionID).name)} ${c.Dirty?'· 未合并':''}</div><h1>${esc(c.Title)}</h1></div><div class="detail-actions">${branch?'<button id="edit-case" class="secondary">编辑</button>':''}<button id="open-record">测试记录</button></div></div><div class="card field-grid"><div class="field"><h4>优先级</h4><p>${esc(c.Priority||'未设置')}</p></div><div class="field"><h4>当前结果</h4><p>${resultName(c.Result)}</p></div><div class="field"><h4>前置条件</h4><p>${esc(c.Preconditions||'—')}</p></div><div class="field"><h4>预期结果</h4><p>${esc(c.Expected||'—')}</p></div><div class="field" style="grid-column:1/-1"><h4>执行步骤</h4><p>${esc(c.Steps||'—')}</p></div></div><div class="card meta-grid"><span>更新者 <b>${esc(c.UpdatedBy)}</b></span><span>更新时间 <b>${fmt(c.UpdatedAt)}</b></span><span>基线版本 <b>r${c.BaseRevision||c.Revision}</b></span></div><div class="card"><h3>编辑历史</h3>${h.map(x=>`<a class="history-row" href="#history=${encodeURIComponent(x.ID)}&amp;version=${encodeURIComponent(c.VersionID)}"><b>${historyAction(x.Action)}</b> · ${esc(x.Author)} <small>${fmt(x.CreatedAt)}${x.SourceVersionID?` · 来源 ${esc(version(x.SourceVersionID)?.name||x.SourceVersionID)}`:''}</small></a>`).join('')||'<p class="meta">暂无编辑历史</p>'}</div>`;if(branch)$('#edit-case').onclick=()=>caseModal(c);$('#open-record').onclick=()=>openRecords(c)}
function descendantFolders(f){let out=[];function walk(id){state.folders.filter(x=>x.VersionID===f.VersionID&&x.ParentID===id).forEach(x=>{out.push(x.ID);walk(x.ID)})}walk(f.ID);return out}
function toggleSelect(v,id,on){if(!selected.has(v))selected.set(v,new Set());on?selected.get(v).add(id):selected.get(v).delete(id);updateBulk()}
function updateBulk(){let entries=[...selected.entries()].filter(([,s])=>s.size);let n=entries.reduce((x,[,s])=>x+s.size,0);$('#bulk').classList.toggle('hidden',!n);$('#selected-count').textContent=`已选 ${n} 项`;}
function folderMenu(v,f){let branch=!version(v).mainline;if(!branch)return f==='root'?[['搜索',()=>openSearch(v,f)],['创建测试版本',versionModal]]:[['搜索',()=>openSearch(v,f)]];return [['查看详情',()=>setFocus({type:'folder',versionID:v,id:f})],['新建文件夹',()=>folderModal(v,f)],['新增用例',()=>caseModal(null,v,f)],['创建测试任务',()=>taskFromFolder(v,f)],['重命名空文件夹',()=>renameModal(v,f)],['搜索此目录',()=>openSearch(v,f)],['导出目录',()=>exportCases(v,f)]]}
function caseMenu(v,id){let c=state.cases.find(x=>x.VersionID===v&&x.ID===id),items=[['查看详情',()=>setFocus({type:'case',versionID:v,id})],['测试记录',()=>openRecords(c)]];if(!version(v).mainline)items.push(['编辑用例',()=>caseModal(c)]);return items}
function menu(e,items){e.preventDefault();let m=$('#context-menu');m.innerHTML=items.map((x,i)=>`<button data-i="${i}">${esc(x[0])}</button>`).join('');m.style.left=Math.min(e.clientX,innerWidth-205)+'px';m.style.top=Math.min(e.clientY,innerHeight-items.length*38-10)+'px';m.classList.remove('hidden');m.querySelectorAll('button').forEach(b=>b.onclick=()=>{m.classList.add('hidden');items[+b.dataset.i][1]()})}
function showModal(title,html,save){$('#modal-title').textContent=title;$('#modal-body').innerHTML=html;modalSave=save;$('#modal').showModal()}
function versionModal(){showModal('创建测试版本',`<label>版本名称<input name="Name" required placeholder="例如：v2.4.0 回归"></label>`,x=>act('createVersion',x))}
function folderModal(v,parent){showModal('新建文件夹',`<label>文件夹名称<input name="Name" required></label>`,x=>act('createFolder',{...x,VersionID:v,ParentID:parent}))}
function renameModal(v,id){let f=state.folders.find(x=>x.VersionID===v&&x.ID===id);showModal('重命名文件夹',`<label>文件夹名称<input name="Name" required value="${esc(f.Name)}"></label>`,x=>act('renameFolder',{...x,VersionID:v,FolderID:id}))}
function caseModal(c,v,f){v=c?.VersionID||v;f=c?.FolderID||f;showModal(c?'编辑测试用例':'新增测试用例',`<label>标题<input name="Title" required value="${esc(c?.Title||'')}"></label><label>优先级<select name="Priority">${['P0','P1','P2','P3'].map(x=>`<option ${c?.Priority===x?'selected':''}>${x}</option>`).join('')}</select></label><label>前置条件<textarea name="Preconditions">${esc(c?.Preconditions||'')}</textarea></label><label>执行步骤<textarea name="Steps">${esc(c?.Steps||'')}</textarea></label><label>预期结果<textarea name="Expected">${esc(c?.Expected||'')}</textarea></label>`,x=>act(c?'editCase':'createCase',{...x,VersionID:v,FolderID:f,CaseID:c?.ID||''}))}
function taskFromFolder(v,f){let ids=cases(v).filter(c=>c.FolderID===f||descendantFolders(state.folders.find(x=>x.VersionID===v&&x.ID===f)).includes(c.FolderID)).map(c=>c.ID);if(!ids.length)return toast('该目录没有用例',true);taskModal(v,ids)}
function taskModal(v,ids){showModal('创建测试任务',`<label>任务名称<input name="Name" required placeholder="例如：登录模块冒烟测试"></label><p class="meta">包含 ${ids.length} 条用例</p>`,x=>act('createTask',{...x,VersionID:v,CaseIDs:ids}))}
function exportCases(v,f){let subs=f?[f,...descendantFolders(state.folders.find(x=>x.VersionID===v&&x.ID===f))]:folders(v).map(x=>x.ID),data=cases(v).filter(c=>subs.includes(c.FolderID));let a=document.createElement('a');a.href=URL.createObjectURL(new Blob([JSON.stringify(data,null,2)],{type:'application/json'}));a.download=`casehub-${version(v).name}.json`;a.click();URL.revokeObjectURL(a.href)}
function openSearch(v='',f=''){let p=$('#search-panel');p.dataset.version=v;p.dataset.folder=f;p.classList.remove('hidden');$('#workspace').style.gridTemplateColumns=`var(--side) 5px 370px minmax(0,1fr)`;$('#search-query').focus()}
function runSearch(){let q=$('#search-query').value.trim().toLowerCase(),field=$('#search-field').value,v=$('#search-panel').dataset.version,f=$('#search-panel').dataset.folder,list=state.cases.filter(c=>(!v||c.VersionID===v));if(f){let fs=[f,...descendantFolders(state.folders.find(x=>x.VersionID===v&&x.ID===f))];list=list.filter(c=>fs.includes(c.FolderID))}list=list.filter(c=>{if(!q)return true;if(field!=='all')return String(c[field]||'').toLowerCase().includes(q);return [c.Title,c.ID,c.Preconditions,c.Steps,c.Expected].some(x=>String(x||'').toLowerCase().includes(q))});let box=$('#search-results');if($('#search-mode').value==='tree'&&v){let ids=new Set(list.map(x=>x.ID));box.innerHTML=tree(v,ids);bindTree(box)}else box.innerHTML=list.map(c=>`<div class="search-hit" data-v="${c.VersionID}" data-c="${c.ID}"><b>${esc(c.Title)}</b><small>${esc(c.ID)} · ${esc(version(c.VersionID).name)}</small></div>`).join('')||'<p class="meta">没有匹配结果</p>';box.querySelectorAll('.search-hit').forEach(e=>e.onclick=()=>setFocus({type:'case',versionID:e.dataset.v,id:e.dataset.c}))}
function renderTasks(){let box=$('#tasks');box.innerHTML=state.tasks.map(t=>`<div class="task"><div class="task-head">▸ ${esc(t.Name)} <small>(${t.CaseIDs.length})</small></div><div class="task-body hidden">${tree(t.VersionID,new Set(t.CaseIDs),t.ID)}</div></div>`).join('')||'<p class="meta" style="padding:15px">暂无测试任务</p>';box.querySelectorAll('.task-head').forEach(e=>e.onclick=()=>e.nextElementSibling.classList.toggle('hidden'));box.querySelectorAll('.task-case').forEach(e=>e.onclick=()=>setFocus({type:'case',versionID:e.dataset.v,id:e.dataset.c,taskID:e.dataset.task}))}
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
$('#modal-close').onclick=$('#modal-cancel').onclick=()=>$('#modal').close();$('#modal-form').onsubmit=async e=>{e.preventDefault();try{await modalSave(Object.fromEntries(new FormData(e.target)));$('#modal').close()}catch{}};$('#drawer-close').onclick=()=>{$('#record-form').reset();recordEditor?.setMarkdown('');$('#drawer').classList.add('hidden')};$('#record-version').onchange=()=>{recordHistoryLimit=3;$('#record-history').replaceChildren();renderRecordHistory()};$('#record-form').onsubmit=async e=>{e.preventDefault();try{await saveRecord('submitRecord')}catch{}};$('#save-draft').onclick=()=>saveRecord('saveRecord').catch(()=>{});$('#close-search').onclick=()=>{$('#search-panel').classList.add('hidden');$('#workspace').style.gridTemplateColumns='var(--side) 5px minmax(0,1fr)'};$('#run-search').onclick=runSearch;
$$('[data-bulk]').forEach(b=>b.onclick=()=>{let entries=[...selected.entries()].find(([,s])=>s.size);if(!entries)return;if(b.dataset.bulk==='export')exportSelected(entries[0],[...entries[1]]);else taskModal(entries[0],[...entries[1]])});function exportSelected(v,ids){let data=cases(v).filter(c=>ids.includes(c.ID)),a=document.createElement('a');a.href=URL.createObjectURL(new Blob([JSON.stringify(data,null,2)],{type:'application/json'}));a.download='casehub-selected.json';a.click()}
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
function historyAction(action){return {create:'新增',edit:'编辑',merge:'合并'}[action]||action}
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
    return `<div class="tree-row folder-row" style="padding-left:${8+depth*17}px" data-req-folder="${f.ID}"><span>▾</span><span>📁</span><span class="label">${esc(f.Name)}</span></div><div>${own.map(d=>reqDocRow(d,depth+1)).join('')}${children.map(ch=>node(ch,depth+1)).join('')}</div>`;
  }
  return reqRoots().map(r=>node(r,0)).join('')||'<p class="meta">暂无需求文档</p>';
}
function reqDocRow(d,depth){return `<div class="tree-row case-row" style="padding-left:${8+depth*17}px" data-req-doc="${d.ID}"><span class="label">📄 ${esc(d.Title)}</span></div>`}
function renderReqTree(){const box=$('#req-tree');box.innerHTML=reqTreeHTML();bindReqTree(box)}
function bindReqTree(root){
  root.querySelectorAll('[data-req-folder]').forEach(e=>{e.onclick=()=>setReqFocus({type:'folder',id:e.dataset.reqFolder});e.oncontextmenu=x=>menu(x,reqFolderMenu(e.dataset.reqFolder))});
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
  const doc=state.reqDocs.find(x=>x.ID===reqFocus.id);
  if(!doc){reqFocus=null;return renderReqFocus()}
  d.innerHTML=`<div class="detail-head"><div><div class="eyebrow">${esc(doc.ID)}</div><h1>${esc(doc.Title)}</h1></div><div class="detail-actions"><button type="button" id="req-ai-design" class="secondary">AI 设计</button><button type="button" id="save-req-doc">保存</button></div></div><div class="card meta-grid"><span>创建者 <b>${esc(doc.CreatedBy)}</b></span><span>创建时间 <b>${fmt(doc.CreatedAt)}</b></span><span>更新者 <b>${esc(doc.UpdatedBy)}</b></span><span>更新时间 <b>${fmt(doc.UpdatedAt)}</b></span></div><div class="card"><div id="req-editor"></div></div>`;
  reqEditor?.destroy();
  reqEditor=new toastui.Editor({el:$('#req-editor'),height:'520px',initialEditType:'wysiwyg',previewStyle:'tab',initialValue:doc.Content||'',language:'zh-CN',theme:document.documentElement.classList.contains('dark')?'dark':'light',usageStatistics:false});
  $('#save-req-doc').onclick=()=>saveReqDoc(doc).catch(()=>{});
  $('#req-ai-design').onclick=()=>openAiDrawer(doc);
}
async function saveReqDoc(doc){
  await act('editReqDoc',{DocID:doc.ID,Title:doc.Title,Content:reqEditor.getMarkdown()});
  renderReqFocus();
  toast('需求文档已保存');
}
function reqFolderMenu(id){return [['查看详情',()=>setReqFocus({type:'folder',id})],['新建文件夹',()=>reqFolderModal(id)],['新增需求文档',()=>reqDocModal(id)],['重命名文件夹',()=>renameReqFolderModal(id)]]}
function reqDocMenu(id){const doc=state.reqDocs.find(x=>x.ID===id);return [['查看详情',()=>setReqFocus({type:'doc',id})],['重命名',()=>renameReqDocModal(doc)],['AI 设计',()=>openAiDrawer(doc)]]}
function reqFolderModal(parent){showModal('新建文件夹',`<label>文件夹名称<input name="Name" required></label>`,x=>act('createReqFolder',{...x,ParentID:parent}))}
function renameReqFolderModal(id){const f=reqFolder(id);showModal('重命名文件夹',`<label>文件夹名称<input name="Name" required value="${esc(f.Name)}"></label>`,x=>act('renameReqFolder',{...x,FolderID:id}))}
function reqDocModal(folderId){showModal('新增需求文档',`<label>标题<input name="Title" required></label>`,x=>act('createReqDoc',{...x,FolderID:folderId,Content:''}))}
function renameReqDocModal(doc){showModal('重命名需求文档',`<label>标题<input name="Title" required value="${esc(doc.Title)}"></label>`,x=>act('editReqDoc',{...x,DocID:doc.ID,Content:doc.Content}))}
function openAiDrawer(doc){aiDoc=doc;$('#ai-drawer-doc').textContent=`${doc.ID} · ${doc.Title}`;$('#ai-drawer').classList.remove('hidden')}
$('#ai-drawer-close').onclick=()=>$('#ai-drawer').classList.add('hidden');

function updateReqEmptyHint(){
  const collapsed=$('#req-sidebar').classList.contains('hidden');
  $('#req-empty-title').textContent=collapsed?'展开侧栏，继续浏览':'从一个需求文档开始';
  $('#req-empty-hint').textContent=collapsed?'还没有选择要查看的内容。展开侧栏后，选择文件夹或需求文档即可查看详情。':'选择左侧的文件夹或需求文档，即可在这里查看详情。';
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

// ---- Top-left app switcher (用例管理 / 需求管理) ----
function setPage(p){
  page=p;
  $$('#app-menu [data-app]').forEach(b=>b.classList.toggle('active',b.dataset.app===p));
  $('#app-menu').classList.add('hidden');
  $('#app-switch-btn').setAttribute('aria-expanded','false');
  const isReq=p==='requirements';
  $('#app-subtitle').textContent=isReq?'需求文档管理':'测试用例管理';
  $('#workspace').classList.toggle('hidden',isReq);
  $('#req-workspace').classList.toggle('hidden',!isReq);
  $('#revision').classList.toggle('hidden',isReq);
  if(isReq)updateReqEmptyHint();
}
$('#app-switch-btn').onclick=e=>{e.stopPropagation();const open=$('#app-menu').classList.toggle('hidden');$('#app-switch-btn').setAttribute('aria-expanded',String(!open))};
$$('#app-menu [data-app]').forEach(b=>b.onclick=()=>setPage(b.dataset.app));
document.addEventListener('click',e=>{if(!e.target.closest('.app-switch'))$('#app-menu').classList.add('hidden')});
