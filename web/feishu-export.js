// Feishu whiteboard's mm-editor-clipboard importer (observed in HAR build 1.0.0.6838).
// Keep this independent of server node IDs, document tokens and whiteboard coordinates.
function buildFeishuMindmap(versions, allFolders, allCases, scopes) {
  let nextID=0, count=0;
  const node=(text,children=[])=>({id:`casehub-${++nextID}`,text:[{type:1,text:String(text??''),style:{}}],children});
  const field=(label,value)=>node(label,[node(value||'未填写')]);
  const caseNode=c=>{count++;return node(`TC：${c.Title}`,[field('优先级',c.Priority),field('前置条件',c.Preconditions),field('执行步骤',c.Steps),field('预期结果',c.Expected)])};
  const roots=scopes.map(scope=>{
    const version=versions.find(v=>v.id===scope.versionID);
    const fs=allFolders.filter(f=>f.VersionID===scope.versionID), fm=new Map(fs.map(f=>[f.ID,f]));
    const cs=allCases.filter(c=>c.VersionID===scope.versionID&&(!scope.ids||scope.ids.includes(c.ID)));
    const visited=new Set();
    function folder(f){
      if(visited.has(f.ID))return null;
      visited.add(f.ID);
      const children=[...cs.filter(c=>c.FolderID===f.ID).map(caseNode),...fs.filter(x=>x.ParentID===f.ID).map(folder).filter(Boolean)];
      return scope.ids&&!children.length?null:node(f.Name,children);
    }
    let children;
    if(scope.folderID){const f=fm.get(scope.folderID);children=f?[folder(f)].filter(Boolean):[]}
    else children=[...fs.filter(f=>!fm.has(f.ParentID)).map(folder).filter(Boolean),...cs.filter(c=>!fm.has(c.FolderID)).map(caseNode)];
    return node(version?.name||scope.versionID,children);
  });
  const root=roots.length===1?roots[0]:node('CaseHub 测试用例',roots);
  const payload={type:'define',data:{structure:'right',globalLineStyle:'curve',nodes:[root]}};
  const lines=[];
  function outline(n,depth){lines.push(...n.text[0].text.split(/\r?\n/).map(line=>'\t'.repeat(depth)+line));n.children.forEach(c=>outline(c,depth+1))}
  outline(root,0);
  const text=lines.join('\n');
  const escapeHTML=s=>s.replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
  const html=`<div class="mm-editor-clipboard" data-json="${escapeHTML(encodeURIComponent(JSON.stringify(payload)))}"><pre>${escapeHTML(text)}</pre></div>`;
  return {html,text,count};
}

async function writeFeishuClipboard(content) {
  // The copy event also preserves HTML on HTTP deployments without Clipboard API.
  let copied=false;
  const onCopy=e=>{
    if(!e.clipboardData)return;
    e.clipboardData.setData('text/html',content.html);
    e.clipboardData.setData('text/plain',content.text);
    e.preventDefault();copied=true;
  };
  document.addEventListener('copy',onCopy);
  try{document.execCommand('copy')}catch{}finally{document.removeEventListener('copy',onCopy)}
  if(copied)return;
  if(navigator.clipboard?.write&&typeof ClipboardItem!=='undefined'){
    await navigator.clipboard.write([new ClipboardItem({
      'text/html':new Blob([content.html],{type:'text/html'}),
      'text/plain':new Blob([content.text],{type:'text/plain'})
    })]);return;
  }
  throw new Error('浏览器未允许复制富文本，请使用 HTTPS 或 localhost 打开，并允许剪贴板访问后重试。');
}

function exportFeishuMindmap(scopes) {
  const content=buildFeishuMindmap(state.versions,state.folders,state.cases,scopes);
  const dialog=document.querySelector('#feishu-export');
  document.querySelector('#feishu-export-preview').textContent=content.text;
  const status=document.querySelector('#feishu-export-status');
  status.textContent=`共 ${content.count} 条用例。`;
  const button=document.querySelector('#feishu-export-copy');
  button.onclick=async()=>{
    button.disabled=true;
    try{await writeFeishuClipboard(content);status.textContent='已复制。请进入飞书白板，点击画布空白处，按 Ctrl+V（Mac：⌘V）粘贴。'}
    catch(e){status.textContent=`复制失败：${e.message}`}
    finally{button.disabled=false}
  };
  document.querySelector('#feishu-export-close').onclick=()=>dialog.close();
  dialog.showModal();
}
