document.documentElement.classList.toggle('dark',localStorage.getItem('casehub-theme')==='dark');
document.querySelector('#theme').onclick=()=>{document.documentElement.classList.toggle('dark');localStorage.setItem('casehub-theme',document.documentElement.classList.contains('dark')?'dark':'light')};

const apiLinks=[...document.querySelectorAll('.api-nav a')];
function navigateAPI(){
  const id=location.hash.slice(1)||'state',target=document.getElementById(id);
  apiLinks.forEach(link=>{
    if(link.hash==='#'+id)link.setAttribute('aria-current','location');
    else link.removeAttribute('aria-current');
  });
  if(!target)return;
  if(target.matches('details'))target.open=true;
  if(location.hash)target.scrollIntoView({block:'start'});
}
apiLinks.forEach(link=>link.addEventListener('click',()=>{
  // Reopen an operation even when its URL is already selected.
  if(link.hash===location.hash)navigateAPI();
}));
window.addEventListener('hashchange',navigateAPI);
navigateAPI();
