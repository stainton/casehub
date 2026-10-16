// 需求文档页「+ 新建文件夹」浏览器回归：位置可选「（顶级）」，新文件夹与现有顶级文件夹并列；默认仍放在当前文件夹下。
// 只对临时 CASEHUB_STORE=memory 服务运行。
const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');
const BASE=process.env.CASEHUB_TEST_URL||'http://127.0.0.1:18081';
(async()=>{
  const browser=await chromium.launch();
  try{
    const page=await browser.newPage({viewport:{width:1500,height:900}}),errors=[];
    page.on('pageerror',e=>errors.push(e.message));
    await page.goto(BASE);
    await page.waitForFunction(()=>typeof state!=='undefined'&&state&&state.reqFolders);
    await page.locator('.page-tab[data-app="requirements"]').click();
    const top=`产品 ${Date.now()}`,child=`版本 ${Date.now()}`;
    const create=async(name,where)=>{
      await page.locator('#create-req-folder').click();
      await page.locator('#modal-body [name="Name"]').fill(name);
      if(where)await page.locator('#modal-body [name="ParentID"]').selectOption(where);
      await page.locator('#modal-form button[type="submit"]').click();
      await page.waitForFunction(n=>state.reqFolders.some(f=>f.Name===n),name);
      return page.evaluate(n=>state.reqFolders.find(f=>f.Name===n),name);
    };
    // 选中一份需求文档时，默认位置是它所在的文件夹
    await page.evaluate(()=>setReqFocus({type:'doc',id:state.reqDocs[0].ID}));
    await page.locator('#create-req-folder').click();
    assert.equal(await page.locator('#modal-body [name="ParentID"]').inputValue(),await page.evaluate(()=>state.reqDocs[0].FolderID));
    assert.equal(await page.locator('#modal-body [name="ParentID"] option').first().innerText(),'（顶级）');
    await page.locator('#modal-cancel').click();

    const p=await create(top,'__top__');
    assert.equal(p.ParentID,'','a top-level folder has no parent');
    const row=page.locator(`#req-tree [data-req-folder="${p.ID}"]`);
    assert.equal(await row.getAttribute('style'),'padding-left:8px','rendered at the top level, beside the existing root');
    // 在新的顶级文件夹下继续建子文件夹
    await page.evaluate(id=>setReqFocus({type:'folder',id}),p.ID);
    const c=await create(child);
    assert.equal(c.ParentID,p.ID,'defaults to the focused folder');
    assert.deepEqual(errors,[]);
    console.log('req folder ok');
  }finally{await browser.close()}
})().catch(e=>{console.error(e);process.exit(1)});
