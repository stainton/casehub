document.documentElement.classList.toggle('dark',localStorage.getItem('casehub-theme')==='dark');
document.querySelector('#theme').onclick=()=>{document.documentElement.classList.toggle('dark');localStorage.setItem('casehub-theme',document.documentElement.classList.contains('dark')?'dark':'light')};
