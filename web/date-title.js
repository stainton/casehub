(() => {
  const docs = document.body.classList.contains('docs-page');
  const title = document.getElementById('app-title');
  const suffix = docs ? 'API 文档' : '测试用例管理';
  function updateDateTitle() {
    const now = new Date();
    const date = `${now.getFullYear()}/${String(now.getMonth() + 1).padStart(2, '0')}/${String(now.getDate()).padStart(2, '0')}`;
    title.textContent = docs ? `${date} · API 文档` : date;
    document.title = `${date} · ${suffix}`;
    const midnight = new Date(now.getFullYear(), now.getMonth(), now.getDate() + 1).getTime();
    setTimeout(updateDateTitle, Math.max(1000, midnight - Date.now() + 50));
  }
  updateDateTitle();
})();
