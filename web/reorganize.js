// ---- 用例重整（功能 AM，临时）：planner 探索产品、把主线用例按 产品 / 模块 / 子级 归类，确认后整体改写主线 ----
// 用例结构问题修复后，连同 internal/core/reorganize.go、index.html 里的 <script> 和 versionHTML 的按钮一起 revert。
// 目录是 全部用例 / 产品 / 模块 / 子功能… / <类别名>，同一叶子目录里的用例编号统一为 TC-<产品>-<功能>-<类别>-NNN。
// 一次任务最多 REORG_BATCH 条用例（每条都带步骤和预期的摘录给 planner 判断它测的是什么），按原目录排序，同一功能的用例
// 尽量落在同一批；没做完的在下一批接着做，已经定下的叶子功能（路径 + 缩写）随请求带过去沿用。
const REORG_SUBJECT='reorganize:main', REORG_FORM_KEY='casehub-reorg-form', REORG_BATCH=300, REORG_EXCERPT=400;
const REORG_CATEGORY={FUNC:'功能测试',REL:'可靠性测试',PERF:'性能测试',SEC:'安全测试',COMPAT:'兼容性测试',UX:'易用性测试'};
// 「重整此目录」：对一个已经重整过的主线目录（模块或子功能）做更深的划分。只把该目录下的用例交给 planner，目录本身和它上面的
// 层级不动，planner 只决定它下面怎么分；编号规则不变，被拆开的目录和新目录各自从 001 编号。
const REORG_ROUND_KEY='casehub-reorg-round'; // 一轮重新细分的开始时间：之后有过重整记录的用例算这一轮已做过；目录重整按目录各记一轮
// reorgTarget：null 是整个主线；否则 {folderID, product, path, label}，path 是该目录的功能路径（不含产品和类别目录）。
let reorgJob=null, reorgSource=null, reorgPayload=null, reorgTarget=null;
const reorgRoundKey=()=>reorgTarget?`${REORG_ROUND_KEY}:${reorgTarget.folderID}`:REORG_ROUND_KEY;
function reorgRound(){try{return localStorage.getItem(reorgRoundKey())||''}catch{return ''}}
function reorgSetRound(v){try{v?localStorage.setItem(reorgRoundKey(),v):localStorage.removeItem(reorgRoundKey())}catch{}}
function reorgDoneInRound(round){
  const done=new Set();
  const since=round&&new Date(round).getTime();
  if(since)for(const h of state.histories||[])if(h.VersionID==='main'&&h.Action==='reorganize'&&new Date(h.CreatedAt).getTime()>=since)done.add(h.CaseID);
  return done;
}
function reorgSubtree(folderID){
  const f=state.folders.find(x=>x.VersionID==='main'&&x.ID===folderID);
  if(!f)return [];
  const ids=new Set([folderID,...descendantFolders(f)]);
  return cases('main').filter(c=>ids.has(c.FolderID));
}
function reorgPool(products,scope){
  if(reorgTarget||scope==='all'){
    const done=reorgDoneInRound(reorgRound());
    return (reorgTarget?reorgSubtree(reorgTarget.folderID):cases('main')).filter(c=>!done.has(c.ID));
  }
  return cases('main').filter(c=>!reorgDone(c,products));
}
// 一个主线目录能否「重整此目录」：它在某个产品目录之下（全部用例 / 产品 / …），产品缩写取自它下面已重整用例的编号。
function reorgFolderTarget(folderID){
  const names=folderPathNames('main',folderID);
  if(names.length<2)return {error:'请在产品目录或更下层的目录上使用「重整此目录」，整个主线请用主线上的「用例重整」'};
  let product='';
  for(const c of reorgSubtree(folderID)){
    const m=AI_CASE_ID_RE.exec(c.ID),n=folderPathNames('main',c.FolderID);
    if(m&&n[1]===names[1]&&n.at(-1)===REORG_CATEGORY[m[3]]){product=m[1];break}
  }
  if(!product)return {error:'这个目录还没按产品重整过，请先用主线上的「用例重整」'};
  const path=names.slice(2);
  if(path.length&&Object.values(REORG_CATEGORY).includes(path.at(-1)))path.pop(); // 类别目录：在它的功能目录下细分
  if(path.length>=6)return {error:'目录已经是最深的功能层级（6 级），不能再往下分'};
  return {folderID,product,path,label:names.slice(1).join(' / ')};
}

function reorgDialog(){
  let d=$('#reorg-dialog');
  if(d)return d;
  d=document.createElement('dialog');d.id='reorg-dialog';d.style.width='min(860px,94vw)';
  d.innerHTML=`<div class="modal-head"><h3 id="reorg-title">用例重整（主线）</h3><button type="button" class="icon-button" id="reorg-close" aria-label="关闭">×</button></div><div id="reorg-body" style="padding:8px 20px 18px;max-height:76vh;overflow:auto"></div>`;
  document.body.appendChild(d);
  $('#reorg-close').onclick=()=>{d.close();reorgSource?.close();reorgSource=null};
  return d;
}
document.addEventListener('click',e=>{if(e.target.closest('[data-reorganize-main]')){e.stopPropagation();openReorg()}});

function openReorgFolder(folderID){
  const target=reorgFolderTarget(folderID);
  if(target.error)return toast(target.error,true);
  openReorg(target);
}
async function openReorg(target=null){
  reorgTarget=target;
  const d=reorgDialog();if(!d.open)d.showModal();
  $('#reorg-title').textContent=target?`重整目录：${target.label}`:'用例重整（主线）';
  const open=state.versions.filter(v=>!v.mainline&&!v.archived);
  if(open.length)return reorgBody(`<p>重整会重建主线目录并给用例改编号。还开着的测试版本以后一合并，旧目录和旧编号就会回到主线，所以要先处理完：</p><ul>${open.map(v=>`<li>${esc(v.name)}（${cases(v.id).filter(c=>c.Dirty).length} 条未合并的修改）</li>`).join('')}</ul><p class="meta">需要保留的修改先「合并」，然后点「完成」把版本归档（或删除）。归档的版本保留旧编号，只读。</p>`);
  reorgBody('<p class="meta">读取任务…</p>');
  let tasks=[];
  try{tasks=(await serviceRequest(`/api/tasks?kind=planner&subject=${encodeURIComponent(REORG_SUBJECT)}&active=1&limit=5`)).tasks||[]}catch(e){return reorgBody(`<p class="meta">${esc(e.message)}</p>`)}
  // 同一时间只处理一个重整任务：没处理完的那个优先，它属于哪个目录记在任务的 meta 里。
  if(tasks[0]){
    const folderID=tasks[0].meta?.folderID;
    if(folderID!==(target?.folderID)){
      const t=folderID?reorgFolderTarget(folderID):null;
      reorgTarget=t&&!t.error?t:null;
      $('#reorg-title').textContent=reorgTarget?`重整目录：${reorgTarget.label}`:'用例重整（主线）';
      if(folderID&&!reorgTarget)toast('有一个目录重整的结果还没处理，但那个目录已经不在了',true);
      else toast('还有一次重整的结果没处理，先处理它');
    }
    return reorgRoute(tasks[0]);
  }
  reorgForm();
}
function reorgBody(html){$('#reorg-body').innerHTML=html}

function reorgProducts(text){
  return text.split('\n').map(l=>l.trim()).filter(Boolean).map(l=>{const [code='',name='',baseUrl='',notes='']=l.split('|').map(x=>x.trim());return {code,name,baseUrl,...(notes?{notes}:{})}});
}
// 一条主线用例已经按现在的规则重整过：位于 全部用例 / <产品名> / 功能路径 / <类别名>，编号是该目录的 TC-<产品>-<功能>-<类别>-NNN。
// 早先只分到模块、没有类别目录的结果不算，会被重新细分。
function reorgDone(c,products){
  const m=AI_CASE_ID_RE.exec(c.ID),p=m&&products.find(x=>x.code===m[1]),names=folderPathNames('main',c.FolderID);
  return !!p&&names[1]===p.name&&names.length>=4&&names.at(-1)===REORG_CATEGORY[m[3]];
}
// 已定下的叶子功能：已重整用例的 路径（去掉产品和类别目录）+ 编号里的功能缩写。整个主线重整时不算本批的用例（它们要重新分）；
// 目录重整时算上（目录本身和它下面已有的功能保留各自的缩写，planner 在此基础上往下分）。
function reorgSettledFunctions(products,batch){
  const inBatch=new Set(reorgTarget?[]:batch.map(c=>c.ID)),seen=new Set(),out=[];
  for(const c of cases('main')){
    if(inBatch.has(c.ID)||!reorgDone(c,products))continue;
    const m=AI_CASE_ID_RE.exec(c.ID),path=folderPathNames('main',c.FolderID).slice(2,-1),key=`${m[1]}:${m[2]}`;
    if(path.length&&!seen.has(key)){seen.add(key);out.push({product:m[1],code:m[2],path})}
  }
  return out;
}
const reorgFolderOf=c=>folderPathNames('main',c.FolderID).slice(1).join(' / ');

function reorgForm(note=''){
  let saved={};try{saved=JSON.parse(localStorage.getItem(REORG_FORM_KEY))||{}}catch{}
  const total=reorgTarget?reorgSubtree(reorgTarget.folderID).length:cases('main').length;
  const intro=reorgTarget?`<p class="meta">planner 只读地探索产品，读这个目录下每条用例测的是什么，在 <b>${esc(reorgTarget.label)}</b> 之下按更细的功能再分一层或几层（如 资产区 / 搜索 → 关键词搜索、标签筛选），最底下仍是测试类别目录。这个目录和它上面的层级不动；被拆开的目录和新目录各自统一编号 <code>TC-${esc(reorgTarget.product)}-&lt;功能&gt;-&lt;类别&gt;-001…</code>。标题、步骤和预期都不改。结果先预览，确认后才写入主线。</p>`:'';
  reorgBody(`${note?`<p>${note}</p>`:''}${intro}<p class="meta"${reorgTarget?' hidden':''}>planner 只读地探索下面的产品（不设计用例），按每条用例实际测的功能，把主线用例归到 <b>全部用例 / 产品 / 模块 / 子功能… / 测试类别</b>（如 资产区 / 搜索 / 功能测试）。同一目录里的用例统一编号为 <code>TC-&lt;产品&gt;-&lt;功能&gt;-&lt;类别&gt;-001…</code>，已有用例的编号也会按目录重排。标题、步骤和预期都不改。结果先预览，确认后才写入主线。</p>
  <form id="reorg-form">
    <label>产品（每行一个：<code>缩写 | 名称 | 地址 | 备注（可选）</code>，缩写 2–12 位大写字母/数字）<textarea name="products" required placeholder="AIGC | AIGC 平台 | https://aigc.example.com/ | 左侧导航是模块入口">${esc(saved.products||'')}</textarea></label>
    <div class="field-grid"><label>测试账号<input name="account" autocomplete="off" value="${esc(saved.account||'')}"></label><label>密码（不保存）<input name="secret" type="password" autocomplete="new-password"></label></div>
    <label>补充说明（可选）<textarea name="instructions" placeholder="例如：素材管理需要切到项目视图才能看到">${esc(saved.instructions||'')}</textarea></label>
    <label>任务超时（分钟）<input name="timeout" type="number" min="1" max="240" value="${esc(saved.timeout||60)}"></label>
    <label${reorgTarget?' hidden':''}>范围<select name="scope"><option value="pending">只做还没重整的用例</option><option value="all"${saved.scope==='all'?' selected':''}>全部主线用例重新细分（已重整的也可能改目录、改编号）</option></select></label>
    <p class="meta" id="reorg-scope"></p>
    <p class="modal-actions" style="padding:0"><button type="submit">开始探索并归类</button></p>
  </form>`);
  const form=$('#reorg-form'),scope=()=>{if(reorgTarget){const pool=reorgPool([],'').length;$('#reorg-scope').textContent=`目录下共 ${total} 条用例。${reorgRound()?`这一轮还剩 ${pool} 条。`:''}本批 ${Math.min(REORG_BATCH,pool)} 条（每批最多 ${REORG_BATCH} 条）。`;return}const products=reorgProducts(form.elements.products.value),left=cases('main').filter(c=>!reorgDone(c,products)).length,all=form.elements.scope.value==='all',pool=reorgPool(products,form.elements.scope.value).length;$('#reorg-scope').textContent=`主线共 ${total} 条用例，其中 ${left} 条还没按现在的规则重整。${all?(reorgRound()?`这一轮重新细分还剩 ${pool} 条。`:'将开始新一轮重新细分。'):''}本批 ${Math.min(REORG_BATCH,pool)} 条（每批最多 ${REORG_BATCH} 条）。`};
  form.elements.products.oninput=scope;form.elements.scope.onchange=scope;scope();
  form.onsubmit=async e=>{
    e.preventDefault();
    const v=Object.fromEntries(new FormData(form)),all=reorgProducts(v.products);
    // 目录重整只涉及该目录的产品，只发这一个产品。
    const products=reorgTarget?all.filter(p=>p.code===reorgTarget.product):all;
    if(reorgTarget&&!products.length)return toast(`请在产品列表里填上 ${reorgTarget.product} 这一行（地址用于探索）`,true);
    const bad=products.find(p=>!/^[A-Z][A-Z0-9]{1,11}$/.test(p.code)||!p.name||p.name.includes('/')||!/^https?:\/\//.test(p.baseUrl));
    if(!products.length||bad)return toast(bad?`产品「${bad.code||bad.name}」格式不对：缩写 2–12 位大写字母/数字，名称不含 /，地址以 http(s):// 开头`:'请至少填写一个产品',true);
    const timeout=Number(v.timeout);
    if(!Number.isInteger(timeout)||timeout<1||timeout>240)return toast('任务超时需为 1–240 分钟的整数',true);
    try{localStorage.setItem(REORG_FORM_KEY,JSON.stringify({...saved,products:v.products,account:v.account,instructions:v.instructions,timeout,...(reorgTarget?{}:{scope:v.scope})}))}catch{}
    // 「全部重新细分」和目录重整按轮进行：第一批记下开始时间，之后每批只取这一轮里还没归类过的用例，全部做完这一轮就结束。
    if((reorgTarget||v.scope==='all')&&!reorgRound())reorgSetRound(new Date().toISOString());
    const pool=reorgPool(products,v.scope);
    const batch=pool.sort((a,b)=>reorgFolderOf(a).localeCompare(reorgFolderOf(b),'zh')||a.ID.localeCompare(b.ID)).slice(0,REORG_BATCH);
    if(!batch.length){reorgSetRound('');return toast(reorgTarget?'这个目录这一轮已经全部重整完了，再点一次开始新的一轮':v.scope==='all'?'这一轮重新细分已经全部完成':'主线用例都已经重整过了')}
    const excerpt=c=>[c.Preconditions&&`前置：${c.Preconditions}`,c.Steps&&`步骤：${c.Steps}`,c.Expected&&`预期：${c.Expected}`].filter(Boolean).join(' ').replace(/\s+/g,' ').slice(0,REORG_EXCERPT);
    const organize={products,cases:batch.map(c=>({id:c.ID,title:c.Title,folder:reorgFolderOf(c),excerpt:excerpt(c)})),functions:reorgSettledFunctions(products,batch),
      ...(reorgTarget?{scope:{product:reorgTarget.product,path:reorgTarget.path}}:{})};
    const productExperience=productExperienceFor(products[0].baseUrl);
    reorgPayload={organize,target:{baseUrl:products[0].baseUrl},timeoutMs:timeout*60000,
      context:{...(v.instructions.trim()?{instructions:v.instructions.trim()}:{}),...(v.account||v.secret?{testData:{username:v.account||'',password:v.secret||''}}:{}),...(productExperience?{productExperience}:{})},
      casehubTask:{subject:REORG_SUBJECT,...(reorgTarget?{meta:{folderID:reorgTarget.folderID}}:{})}};
    await reorgSubmit(reorgPayload);
  };
}
async function reorgSubmit(payload){
  try{reorgRoute(await serviceRequest('/api/planner/jobs',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(payload)}))}
  catch(e){toast(e.message,true)}
}

function reorgRoute(job){
  reorgJob=job;
  if(job.status==='succeeded')return reorgPreview(job);
  if(['failed','cancelled'].includes(job.status))return reorgFailed(job);
  reorgBody(`<div class="card meta-grid"><span>状态 <b id="reorg-stage">${esc(job.stage||job.status)}</b></span><span>任务 ${esc(job.id)}</span></div><pre id="reorg-log" style="max-height:46vh;overflow:auto;white-space:pre-wrap;font-size:12px"></pre><p class="modal-actions" style="padding:0"><button type="button" class="secondary" id="reorg-cancel">取消任务</button></p>`);
  $('#reorg-cancel').onclick=async()=>{try{await serviceRequest(`/api/planner/jobs/${job.id}`,{method:'DELETE'})}catch(e){toast(e.message,true)}};
  reorgSource?.close();
  const source=reorgSource=new EventSource(`/api/planner/jobs/${job.id}/events`);
  const finish=s=>{if(['succeeded','failed','cancelled'].includes(s)){source.close();if(reorgSource===source)reorgSource=null;serviceRequest(`/api/planner/jobs/${job.id}`).then(reorgRoute)}};
  source.addEventListener('snapshot',e=>{const x=JSON.parse(e.data);if($('#reorg-stage'))$('#reorg-stage').textContent=x.stage||x.status;finish(x.status)});
  source.addEventListener('progress',e=>{
    const x=JSON.parse(e.data),log=$('#reorg-log');
    if(log){log.textContent+=`[${fmt(x.createdAt)}] ${x.stage||''} ${x.message||''}\n`;log.scrollTop=log.scrollHeight}
    if($('#reorg-stage')&&x.stage)$('#reorg-stage').textContent=x.stage;
    finish(x.status);
  });
}
function reorgFailed(job){
  const canContinue=job.continuable&&reorgPayload;
  reorgBody(`<p>任务${job.status==='cancelled'?'已取消':'失败'}${job.error?`：${esc(job.error.code)} ${esc(job.error.message)}`:''}</p>${job.continuable&&!reorgPayload?'<p class="meta">中断前的探索还在，但「继续」需要重新提交同一份请求（含密码），页面刷新后已经没有了，只能重新开始。</p>':''}<p class="modal-actions" style="padding:0"><button type="button" class="secondary" id="reorg-restart">重新开始</button>${canContinue?'<button type="button" id="reorg-continue">继续</button>':''}</p>`);
  $('#reorg-restart').onclick=()=>{markTaskHandled('planner',job.id);reorgForm()};
  if(canContinue)$('#reorg-continue').onclick=()=>{markTaskHandled('planner',job.id);reorgSubmit({...reorgPayload,continueFrom:job.id})};
}

async function reorgPreview(job){
  let r;
  try{r=await serviceRequest(`/api/planner/jobs/${job.id}/result`)}catch(e){return reorgBody(`<p class="meta">读取结果失败：${esc(e.message)}</p>`)}
  if(r.mode!=='organize')return reorgBody('<p class="meta">这个任务的结果不是用例重整结果。</p>');
  const names=Object.fromEntries(r.products.map(p=>[p.code,p.name])),byCase=new Map(cases('main').map(c=>[c.ID,c]));
  const live=r.assignments.filter(a=>byCase.has(a.caseId)),gone=r.assignments.length-live.length;
  const groups=new Map();
  // 一个叶子目录 = 产品 / 功能路径 / 类别，目录里的用例统一编号 TC-<产品>-<功能>-<类别>-001…
  for(const a of live){const key=[names[a.product],...a.path,REORG_CATEGORY[a.category]||a.category].join(' / ');if(!groups.has(key))groups.set(key,{prefix:`TC-${a.product}-${a.functionCode}-${a.category}`,list:[]});groups.get(key).list.push(a)}
  const rows=[...groups].sort(([x],[y])=>x.localeCompare(y,'zh')).map(([path,{prefix,list}])=>`<details><summary><b>${esc(path)}</b> · ${list.length} 条 · <code>${esc(prefix)}-NNN</code></summary><ul style="margin:4px 0 8px">${list.map(a=>{const c=byCase.get(a.caseId);return `<li><code>${esc(a.caseId)}</code> ${esc(c.Title)} <small class="meta">（原目录：${esc(reorgFolderOf(c)||'根目录')}）</small></li>`}).join('')}</ul></details>`).join('');
  const lim=(r.limitations||[]).map(l=>`<li>[${esc(l.risk)}] ${esc(l.summary)}</li>`).join('');
  reorgBody(`<p>归类了 <b>${live.length}</b> 条用例，分到 <b>${groups.size}</b> 个目录。${r.unassigned?.length?`另有 ${r.unassigned.length} 条 planner 没归类，保持原位。`:''}${gone?`${gone} 条已经不在主线，忽略。`:''}</p>
    <p class="meta">应用后：主线目录按下面重建（同名目录复用），每个目录里的用例（连同之前批次已经放进去的）统一按该目录的前缀从 001 重新编号，主线上的历史、执行记录、脚本和测试任务跟着改；没有用例的旧目录（包括 V1/V2 这类版本目录）全部删除。<b>不可撤销。</b></p>
    ${lim?`<div class="card"><b>未验证的范围</b><ul>${lim}</ul></div>`:''}
    <div class="card">${rows||'<p class="meta">没有可应用的归类。</p>'}</div>
    <p class="modal-actions" style="padding:0"><button type="button" class="secondary" id="reorg-discard">放弃这次结果</button><button type="button" class="danger" id="reorg-apply"${live.length?'':' disabled'}>应用到主线</button></p>`);
  $('#reorg-discard').onclick=()=>{markTaskHandled('planner',job.id);reorgForm()};
  $('#reorg-apply').onclick=async()=>{
    if(!confirm(`确定改写主线？${live.length} 条用例会移到 ${groups.size} 个目录并按目录重新编号，空出来的旧目录会被删除，无法撤销。`))return;
    const btn=$('#reorg-apply');btn.disabled=true;
    try{await act('reorganizeMainline',{Reorganize:{products:r.products,assignments:live}})}catch{btn.disabled=false;return}
    markTaskHandled('planner',job.id);
    if(r.productExperience?.origin&&r.productExperience.notes)act('recordProductExploration',{Origin:r.productExperience.origin,ExplorationNotes:r.productExperience.notes}).catch(()=>{});
    const products=r.products.map(p=>({...p})),round=reorgRound(),left=reorgPool(products,round?'all':'pending').length;
    if(!left&&round)reorgSetRound('');
    if(reorgTarget){reorgForm(left?`已写入主线。这个目录这一轮还有 ${left} 条用例没做，可以接着跑下一批。`:'已写入主线，这个目录已经重整完。');return}
    reorgForm(left?`已写入主线。${round?'这一轮重新细分':''}还有 ${left} 条用例没做，可以接着跑下一批。`:'已写入主线，所有用例都已重整。');
  };
}
