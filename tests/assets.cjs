// 资产回归：头像菜单、上传/预览/删除，以及 AI 设计抽屉勾选资产后随任务下发。只对临时 CASEHUB_STORE=memory 服务运行。
// 自带假 planner（记录收到的 job 请求，并像真的一样缓存 CaseHub 推来的资产），启动 CaseHub 时 CASEHUB_PLANNER_URL 指向它：
//   CASEHUB_STORE=memory PORT=18081 CASEHUB_PLANNER_URL=http://127.0.0.1:4597 go run .
const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');
const http=require('node:http');
const {randomUUID}=require('node:crypto');
const base=process.env.CASEHUB_TEST_URL||'http://127.0.0.1:18081';
const PORT=Number(process.env.CASEHUB_ASSETS_PLANNER_PORT||4597);
const jobs=[],pushed=new Map();
const planner=http.createServer((req,res)=>{
  const chunks=[];req.on('data',c=>chunks.push(c));req.on('end',()=>{const raw=Buffer.concat(chunks),body=raw.toString('utf8');
    const json=(s,o)=>{res.writeHead(s,{'Content-Type':'application/json'});res.end(JSON.stringify(o))};
    if(req.url.startsWith('/v1/planner/assets/')){const sha=req.url.split('/').pop();
      if(req.method==='HEAD'){res.writeHead(pushed.has(sha)?200:404);return res.end()}
      if(req.method==='PUT'){pushed.set(sha,raw);res.writeHead(201);return res.end()}}
    if(req.method==='GET'&&req.url==='/v1/planner/jobs/x')return json(404,{error:{code:'NOT_FOUND',message:'x'}});
    if(req.method==='POST'&&req.url==='/v1/planner/estimate'){const i=JSON.parse(body);return json(200,{suggestedCaseCount:6,rationale:'r',requirementCodes:[{requirement:i.requirements[0].id,code:`A${Date.now().toString(36).toUpperCase().slice(-6)}`}]})}
    if(req.method==='POST'&&req.url==='/v1/planner/jobs'){
      jobs.push(JSON.parse(body));const at=new Date().toISOString();
      return json(202,{id:randomUUID(),kind:'planner',status:'failed',stage:'failed',createdAt:at,updatedAt:at,finishedAt:at,lastEventId:1,error:{code:'X',message:'test stop'}});
    }
    json(404,{error:{code:'NOT_FOUND',message:req.url}});
  });
});
const PNG=Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==','base64');
(async()=>{
 await new Promise(r=>planner.listen(PORT,'127.0.0.1',r));
 const browser=await chromium.launch({headless:true,args:['--no-sandbox']});
 try{
  const page=await (await browser.newContext({viewport:{width:1280,height:900}})).newPage(),errors=[];
  page.on('pageerror',e=>errors.push(e.message));
  await page.goto(base);await page.locator('.folder-row').first().waitFor();
  // 设置不再是独立按钮，只在头像菜单里。
  assert.equal(await page.locator('#settings-open').count(),0);
  await page.locator('#avatar-menu').click();
  const items=await page.locator('#context-menu button').allInnerTexts();
  assert.ok(items.some(t=>t.includes('设置'))&&items.some(t=>t.includes('资产')),items.join());
  await page.locator('#context-menu button',{hasText:'资产'}).click();
  await page.locator('#asset-form').waitFor();
  assert.deepEqual(await page.locator('#assets-types button').allInnerTexts(),['图片','视频','音频']);
  // 上传一张图片并出现在列表里。
  await page.locator('#asset-form [name="file"]').setInputFiles({name:'pixel.png',mimeType:'image/png',buffer:PNG});
  await page.locator('#asset-form [name="name"]').fill('测试像素图');
  await page.locator('#asset-form [type="submit"]').click();
  await page.locator('.asset-item',{hasText:'测试像素图'}).waitFor();
  // 类型互不串：视频页看不到这张图。
  await page.locator('[data-assets-type="video"]').click();
  assert.equal(await page.locator('.asset-item').count(),0);
  await page.locator('[data-assets-type="image"]').click();
  const list=await (await page.request.get(`${base}/api/assets?type=image`)).json();
  const asset=list.find(a=>a.name==='测试像素图');assert.ok(asset);
  assert.deepEqual([...await (await page.request.get(`${base}/api/assets/${asset.id}`)).body()],[...PNG]);
  await page.locator('#assets-close').click();
  // AI 设计抽屉勾选资产 → 假 planner 先收到推送的字节，job 里是 context.assets（哈希引用），没有 assetIds 也没有 URL。
  const id=await page.evaluate(async()=>{const title=`资产验证 ${Date.now()}`;await act('createReqDoc',{FolderID:'req-root',Title:title,Content:'上传头像'});return state.reqDocs.find(d=>d.Title===title).ID});
  await page.locator('[data-app="requirements"]').click();await page.locator(`[data-req-doc="${id}"]`).click();await page.locator('#req-ai-design').click();
  await page.locator('#ai-agent').selectOption('playwright');
  await page.locator('#ai-form [name="baseUrl"]').fill('http://127.0.0.1:18081');
  await page.locator('#ai-form [name="assetIds"]').first().check();
  await page.locator('#ai-form [type="submit"]').click();           // 开始分析
  await page.locator('#ai-form [name="caseCount"]:not([disabled])').waitFor();
  assert.equal(await page.locator('#ai-form [name="assetIds"]').first().isChecked(),true); // 评估后勾选保留
  await page.locator('#ai-form [type="submit"]').click();           // 确认并开始设计
  await page.waitForFunction(()=>true);
  for(let i=0;i<50&&!jobs.length;i++)await new Promise(r=>setTimeout(r,100));
  assert.equal(jobs.length,1);
  const ctx=jobs[0].context;
  assert.equal(ctx.assetIds,undefined);
  assert.equal(ctx.assets.length,1);
  // 只带引用（哈希+大小），字节由 CaseHub 先推给 agent；不给 agent 任何回调地址。
  assert.equal(ctx.assets[0].id,asset.id);assert.equal(ctx.assets[0].name,'测试像素图');assert.equal(ctx.assets[0].size,PNG.length);
  assert.equal(ctx.assets[0].url,undefined);
  assert.deepEqual([...pushed.get(ctx.assets[0].sha256)],[...PNG]);
  // 删除。
  await page.locator('#avatar-menu').click();await page.locator('#context-menu button',{hasText:'资产'}).click();
  page.once('dialog',d=>d.accept());
  await page.locator('.asset-item',{hasText:'测试像素图'}).locator('[data-asset-delete]').click();
  await page.locator('#assets-empty').waitFor();
  assert.equal((await page.request.get(`${base}/api/assets/${asset.id}`)).status(),404);
  assert.deepEqual(errors,[]);console.log('资产回归通过');
 }finally{await browser.close();planner.close()}
})().catch(e=>{console.error(e);process.exit(1)});
