// ---- 用例重整（功能 AM，临时）：planner 探索产品、把主线用例按 产品 / 模块 / 子级 归类，确认后整体改写主线 ----
// 用例结构问题修复后，连同 internal/core/reorganize.go、index.html 里的 <script> 和 versionHTML 的按钮一起 revert。
// 一次任务最多 REORG_BATCH 条用例；没重整完的，在下一批里接着做，已经定下的模块缩写会随请求带过去沿用。
const REORG_SUBJECT='reorganize:main', REORG_FORM_KEY='casehub-reorg-form', REORG_BATCH=800, REORG_EXCERPT=200;
const REORG_CATEGORY={FUNC:'功能',REL:'可靠性',PERF:'性能',SEC:'安全',COMPAT:'兼容性',UX:'易用性'};
let reorgJob=null, reorgSource=null, reorgPayload=null; // 进行中的任务、它的进度订阅、提交时的请求（含密码，只在内存里，供「继续」重发）

function reorgDialog(){
  let d=$('#reorg-dialog');
  if(d)return d;
  d=document.createElement('dialog');d.id='reorg-dialog';d.style.width='min(860px,94vw)';
  d.innerHTML=`<div class="modal-head"><h3>用例重整（主线）</h3><button type="button" class="icon-button" id="reorg-close" aria-label="关闭">×</button></div><div id="reorg-body" style="padding:8px 20px 18px;max-height:76vh;overflow:auto"></div>`;
  document.body.appendChild(d);
  $('#reorg-close').onclick=()=>{d.close();reorgSource?.close();reorgSource=null};
  return d;
}
document.addEventListener('click',e=>{if(e.target.closest('[data-reorganize-main]')){e.stopPropagation();openReorg()}});

async function openReorg(){
  const d=reorgDialog();if(!d.open)d.showModal();
  const open=state.versions.filter(v=>!v.mainline&&!v.archived);
  if(open.length)return reorgBody(`<p>重整会重建主线目录并给用例改编号。还开着的测试版本以后一合并，旧目录和旧编号就会回到主线，所以要先处理完：</p><ul>${open.map(v=>`<li>${esc(v.name)}（${cases(v.id).filter(c=>c.Dirty).length} 条未合并的修改）</li>`).join('')}</ul><p class="meta">需要保留的修改先「合并」，然后点「完成」把版本归档（或删除）。归档的版本保留旧编号，只读。</p>`);
  reorgBody('<p class="meta">读取任务…</p>');
  let tasks=[];
  try{tasks=(await serviceRequest(`/api/tasks?kind=planner&subject=${encodeURIComponent(REORG_SUBJECT)}&active=1&limit=5`)).tasks||[]}catch(e){return reorgBody(`<p class="meta">${esc(e.message)}</p>`)}
  if(tasks[0])return reorgRoute(tasks[0]);
  reorgForm();
}
function reorgBody(html){$('#reorg-body').innerHTML=html}

function reorgProducts(text){
  return text.split('\n').map(l=>l.trim()).filter(Boolean).map(l=>{const [code='',name='',baseUrl='',notes='']=l.split('|').map(x=>x.trim());return {code,name,baseUrl,...(notes?{notes}:{})}});
}
// 一条主线用例已经重整过：编号是 TC-<产品>-… 且位于 全部用例 / <产品名> 之下。
function reorgDone(c,products){
  const m=AI_CASE_ID_RE.exec(c.ID),p=m&&products.find(x=>x.code===m[1]);
  return !!p&&folderPathNames('main',c.FolderID)[1]===p.name;
}
function reorgSettledModules(products){
  const seen=new Set(),out=[];
  for(const c of cases('main')){
    if(!reorgDone(c,products))continue;
    const m=AI_CASE_ID_RE.exec(c.ID),name=folderPathNames('main',c.FolderID)[2],key=`${m[1]}:${m[2]}`;
    if(name&&!seen.has(key)){seen.add(key);out.push({product:m[1],code:m[2],name})}
  }
  return out;
}

function reorgForm(note=''){
  let saved={};try{saved=JSON.parse(localStorage.getItem(REORG_FORM_KEY))||{}}catch{}
  const total=cases('main').length;
  reorgBody(`${note?`<p>${note}</p>`:''}<p class="meta">planner 只读地探索下面的产品（不设计用例），把主线用例归到 <b>全部用例 / 产品 / 模块 / 按需的子级</b>，并改编号为 <code>TC-&lt;产品&gt;-&lt;模块&gt;-&lt;类别&gt;-NNN</code>。标题、步骤和预期都不改。结果先预览，确认后才写入主线。</p>
  <form id="reorg-form">
    <label>产品（每行一个：<code>缩写 | 名称 | 地址 | 备注（可选）</code>，缩写 2–12 位大写字母/数字）<textarea name="products" required placeholder="AIGC | AIGC 平台 | https://aigc.example.com/ | 左侧导航是模块入口">${esc(saved.products||'')}</textarea></label>
    <div class="field-grid"><label>测试账号<input name="account" autocomplete="off" value="${esc(saved.account||'')}"></label><label>密码（不保存）<input name="secret" type="password" autocomplete="new-password"></label></div>
    <label>补充说明（可选）<textarea name="instructions" placeholder="例如：素材管理需要切到项目视图才能看到">${esc(saved.instructions||'')}</textarea></label>
    <label>任务超时（分钟）<input name="timeout" type="number" min="1" max="240" value="${esc(saved.timeout||60)}"></label>
    <p class="meta" id="reorg-scope">主线共 ${total} 条用例。</p>
    <p class="modal-actions" style="padding:0"><button type="submit">开始探索并归类</button></p>
  </form>`);
  const form=$('#reorg-form'),scope=()=>{const products=reorgProducts(form.elements.products.value),left=cases('main').filter(c=>!reorgDone(c,products)).length;$('#reorg-scope').textContent=`主线共 ${total} 条用例，其中 ${left} 条还没重整；本批最多 ${REORG_BATCH} 条。`};
  form.elements.products.oninput=scope;scope();
  form.onsubmit=async e=>{
    e.preventDefault();
    const v=Object.fromEntries(new FormData(form)),products=reorgProducts(v.products);
    const bad=products.find(p=>!/^[A-Z][A-Z0-9]{1,11}$/.test(p.code)||!p.name||p.name.includes('/')||!/^https?:\/\//.test(p.baseUrl));
    if(!products.length||bad)return toast(bad?`产品「${bad.code||bad.name}」格式不对：缩写 2–12 位大写字母/数字，名称不含 /，地址以 http(s):// 开头`:'请至少填写一个产品',true);
    const timeout=Number(v.timeout);
    if(!Number.isInteger(timeout)||timeout<1||timeout>240)return toast('任务超时需为 1–240 分钟的整数',true);
    try{localStorage.setItem(REORG_FORM_KEY,JSON.stringify({products:v.products,account:v.account,instructions:v.instructions,timeout}))}catch{}
    const batch=cases('main').filter(c=>!reorgDone(c,products)).slice(0,REORG_BATCH);
    if(!batch.length)return toast('主线用例都已经重整过了');
    const organize={products,cases:batch.map(c=>({id:c.ID,title:c.Title,folder:folderPathNames('main',c.FolderID).slice(1).join(' / '),excerpt:[c.Preconditions,c.Steps].filter(Boolean).join(' ').replace(/\s+/g,' ').slice(0,REORG_EXCERPT)})),modules:reorgSettledModules(products)};
    const productExperience=productExperienceFor(products[0].baseUrl);
    reorgPayload={organize,target:{baseUrl:products[0].baseUrl},timeoutMs:timeout*60000,
      context:{...(v.instructions.trim()?{instructions:v.instructions.trim()}:{}),...(v.account||v.secret?{testData:{username:v.account||'',password:v.secret||''}}:{}),...(productExperience?{productExperience}:{})},
      casehubTask:{subject:REORG_SUBJECT}};
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
  for(const a of live){const key=[names[a.product],...a.folders].join(' / ');if(!groups.has(key))groups.set(key,[]);groups.get(key).push(a)}
  const rows=[...groups].sort(([x],[y])=>x.localeCompare(y,'zh')).map(([path,list])=>`<details><summary><b>${esc(path)}</b> · ${list.length} 条</summary><ul style="margin:4px 0 8px">${list.map(a=>{const c=byCase.get(a.caseId);return `<li><code>${esc(a.caseId)}</code> → <code>TC-${esc(a.product)}-${esc(a.moduleCode)}-${esc(a.category)}-…</code> ${esc(c.Title)} <small class="meta">（原目录：${esc(folderPathNames('main',c.FolderID).slice(1).join(' / ')||'根目录')}；${REORG_CATEGORY[a.category]||a.category}）</small></li>`}).join('')}</ul></details>`).join('');
  const lim=(r.limitations||[]).map(l=>`<li>[${esc(l.risk)}] ${esc(l.summary)}</li>`).join('');
  reorgBody(`<p>归类了 <b>${live.length}</b> 条用例，分到 <b>${groups.size}</b> 个目录。${r.unassigned?.length?`另有 ${r.unassigned.length} 条 planner 没归类，保持原位。`:''}${gone?`${gone} 条已经不在主线，忽略。`:''}</p>
    <p class="meta">应用后：主线目录按下面重建（同名目录复用），这些用例改成新编号（编号末尾的序号由 CaseHub 分配，接着已有的号往后排），主线上的历史、执行记录、脚本和测试任务跟着改；没有用例的旧目录（包括 V1/V2 这类版本目录）全部删除。<b>不可撤销。</b></p>
    ${lim?`<div class="card"><b>未验证的范围</b><ul>${lim}</ul></div>`:''}
    <div class="card">${rows||'<p class="meta">没有可应用的归类。</p>'}</div>
    <p class="modal-actions" style="padding:0"><button type="button" class="secondary" id="reorg-discard">放弃这次结果</button><button type="button" class="danger" id="reorg-apply"${live.length?'':' disabled'}>应用到主线</button></p>`);
  $('#reorg-discard').onclick=()=>{markTaskHandled('planner',job.id);reorgForm()};
  $('#reorg-apply').onclick=async()=>{
    if(!confirm(`确定改写主线？${live.length} 条用例会改编号并移到新目录，空出来的旧目录会被删除，无法撤销。`))return;
    const btn=$('#reorg-apply');btn.disabled=true;
    try{await act('reorganizeMainline',{Reorganize:{products:r.products,assignments:live}})}catch{btn.disabled=false;return}
    markTaskHandled('planner',job.id);
    if(r.productExperience?.origin&&r.productExperience.notes)act('recordProductExploration',{Origin:r.productExperience.origin,ExplorationNotes:r.productExperience.notes}).catch(()=>{});
    const products=r.products.map(p=>({...p})),left=cases('main').filter(c=>!reorgDone(c,products)).length;
    reorgForm(left?`已写入主线。还有 ${left} 条用例没重整，可以接着跑下一批。`:'已写入主线，所有用例都已重整。');
  };
}
