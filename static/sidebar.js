(function() {
  var STORAGE_KEY='serve-sidebar';
  var sidebar=document.getElementById('serve-sidebar');
  var toggle=document.getElementById('serve-sidebar-toggle');
  var tree=document.getElementById('serve-sidebar-tree');
  var currentPath=window.__servePath||'';
  function getState(){try{return JSON.parse(localStorage.getItem(STORAGE_KEY)||'{}');}catch(e){return{};}}
  function saveState(s){try{localStorage.setItem(STORAGE_KEY,JSON.stringify(s));}catch(e){}}
  var state=getState();
  if(state.hidden){sidebar.classList.add('collapsed');toggle.classList.add('collapsed');document.body.classList.add('sidebar-collapsed');document.body.classList.remove('has-sidebar');toggle.textContent='☰';}
  else{document.body.classList.add('has-sidebar');toggle.textContent='‹';}
  toggle.addEventListener('click',function(){var s=getState();s.hidden=!s.hidden;saveState(s);sidebar.classList.toggle('collapsed');toggle.classList.toggle('collapsed');document.body.classList.toggle('has-sidebar');document.body.classList.toggle('sidebar-collapsed');toggle.textContent=s.hidden?'☰':'‹';});
  function fileIcon(name){var ext=name.split('.').pop().toLowerCase();var icons={md:'📄',html:'🌐',htm:'🌐',pdf:'📁',json:'{ }',yaml:'⚙',yml:'⚙',py:'🐍',js:'JS',ts:'TS',css:'🎨',txt:'📃',log:'📃',xml:'✂',csv:'📊',toml:'⚙',svg:'🖼'};return icons[ext]||'📄';}
  var activeAncestors={};
  if(currentPath){var parts=currentPath.split('/');for(var i=1;i<parts.length;i++){activeAncestors[parts.slice(0,i).join('/')]=true;}}
  function esc(s){var d=document.createElement('div');d.textContent=s;return d.innerHTML;}
  function renderTree(items,depth){
    var s=getState();var html='';
    items.forEach(function(item){
      var pad='padding-left:'+(12+depth*16)+'px;';
      if(item.type==='dir'){
        var dirKey='dir:'+item.path;var stored=s[dirKey];
        var isOpen=stored===true||(stored===undefined&&activeAncestors[item.path]===true);
        html+='<div class="sidebar-dir'+(isOpen?' open':'')+'" style="'+pad+'" data-dir="'+item.path+'">'+esc(item.name)+'</div>';
        html+='<div class="sidebar-children'+(isOpen?'':' collapsed')+'" data-dir-children="'+item.path+'">';
        html+=renderTree(item.children||[],depth+1);
        html+='</div>';
      } else {
        var isActive=item.path===currentPath;
        html+='<a class="sidebar-file'+(isActive?' active':'')+'" href="/'+encodeURI(item.path)+'" style="'+pad+'"><span class="sidebar-icon">'+fileIcon(item.name)+'</span>'+esc(item.name)+'</a>';
      }
    });
    return html;
  }
  function renderSidebar(files){
    tree.innerHTML=renderTree(files,0);
    tree.addEventListener('click',function(e){
      var dir=e.target.closest('.sidebar-dir');if(!dir)return;
      var key='dir:'+dir.getAttribute('data-dir');
      var children=tree.querySelector('[data-dir-children="'+dir.getAttribute('data-dir')+'"]');
      if(!children)return;
      dir.classList.toggle('open');children.classList.toggle('collapsed');
      var s=getState();s[key]=dir.classList.contains('open');saveState(s);
    });
    var active=tree.querySelector('.sidebar-file.active');
    if(active)active.scrollIntoView({block:'center'});
  }
  window.__updateSidebarTree=function(files){renderSidebar(files);};
  if(window.__serveFileTree){
    renderSidebar(window.__serveFileTree);
  } else {
    fetch('/api/files').then(function(r){return r.json();}).then(function(data){renderSidebar(data.files||[]);});
  }
})();