(function() {
  var STORAGE_KEY='serve-sidebar';
  var sidebar=document.getElementById('serve-sidebar');
  var toggle=document.getElementById('serve-sidebar-toggle');
  var tree=document.getElementById('serve-sidebar-tree');
  var currentPath=window.__servePath||'';
  // "Go up a directory": ask the server to re-root at the parent, then navigate
  // to the same file under the new root (its path gains the old root's name).
  var upBtn=document.getElementById('serve-sidebar-up');
  if(upBtn)upBtn.addEventListener('click',function(){
    fetch('/api/reroot',{method:'POST'}).then(function(r){return r.json();}).then(function(d){
      if(!d||!d.ok)return;
      if(!currentPath){location.href='/';return;}
      location.href='/'+encodeURI(d.prefix+'/'+currentPath);
    }).catch(function(){});
  });
  function getState(){try{return JSON.parse(localStorage.getItem(STORAGE_KEY)||'{}');}catch(e){return{};}}
  function saveState(s){try{localStorage.setItem(STORAGE_KEY,JSON.stringify(s));}catch(e){}}
  var state=getState();
  var MIN_W=180,MAX_W=600;
  function applyWidth(w){document.documentElement.style.setProperty('--serve-sidebar-w',w+'px');}
  if(typeof state.width==='number'&&state.width>=MIN_W&&state.width<=MAX_W)applyWidth(state.width);
  if(state.hidden){sidebar.classList.add('collapsed');toggle.classList.add('collapsed');document.body.classList.add('sidebar-collapsed');document.body.classList.remove('has-sidebar');toggle.textContent='☰';}
  else{document.body.classList.add('has-sidebar');toggle.textContent='‹';}
  toggle.addEventListener('click',function(){var s=getState();s.hidden=!s.hidden;saveState(s);sidebar.classList.toggle('collapsed');toggle.classList.toggle('collapsed');document.body.classList.toggle('has-sidebar');document.body.classList.toggle('sidebar-collapsed');toggle.textContent=s.hidden?'☰':'‹';});
  var resize=document.createElement('div');
  resize.id='serve-sidebar-resize';
  if(state.hidden)resize.classList.add('hidden');
  document.body.appendChild(resize);
  toggle.addEventListener('click',function(){resize.classList.toggle('hidden',getState().hidden===true);});
  resize.addEventListener('mousedown',function(e){
    e.preventDefault();
    var startX=e.clientX;
    var startW=parseInt(getComputedStyle(document.documentElement).getPropertyValue('--serve-sidebar-w'),10)||260;
    resize.classList.add('dragging');
    document.body.classList.add('serve-resizing');
    function onMove(ev){
      var w=startW+(ev.clientX-startX);
      if(w<MIN_W)w=MIN_W;else if(w>MAX_W)w=MAX_W;
      applyWidth(w);
    }
    function onUp(){
      document.removeEventListener('mousemove',onMove);
      document.removeEventListener('mouseup',onUp);
      resize.classList.remove('dragging');
      document.body.classList.remove('serve-resizing');
      var w=parseInt(getComputedStyle(document.documentElement).getPropertyValue('--serve-sidebar-w'),10);
      var s=getState();s.width=w;saveState(s);
    }
    document.addEventListener('mousemove',onMove);
    document.addEventListener('mouseup',onUp);
  });
  resize.addEventListener('dblclick',function(){applyWidth(260);var s=getState();s.width=260;saveState(s);});
  function fileIcon(name){var ext=name.split('.').pop().toLowerCase();var icons={md:'📄',html:'🌐',htm:'🌐',pdf:'📁',json:'{ }',yaml:'⚙',yml:'⚙',py:'🐍',js:'JS',ts:'TS',css:'🎨',txt:'📃',log:'📃',xml:'✂',csv:'📊',toml:'⚙',svg:'🖼'};return icons[ext]||'📄';}
  var activeAncestors={};
  if(currentPath){var parts=currentPath.split('/');for(var i=1;i<parts.length;i++){activeAncestors[parts.slice(0,i).join('/')]=true;}}
  function esc(s){var d=document.createElement('div');d.textContent=s;return d.innerHTML;}
  // Current-file meta block: filename + created/modified dates in the header.
  (function(){
    var info=window.__serveFile;if(!info)return;
    var header=document.getElementById('serve-sidebar-header');if(!header)return;
    var meta=document.createElement('div');meta.id='serve-file-meta';
    var h='<div class="file-meta-name" title="'+esc(info.name)+'">'+esc(info.name)+'</div>';
    if(info.created)h+='<div class="file-meta-row"><span class="file-meta-label">Created</span><span>'+esc(info.created)+'</span></div>';
    if(info.modified)h+='<div class="file-meta-row"><span class="file-meta-label">Modified</span><span>'+esc(info.modified)+'</span></div>';
    meta.innerHTML=h;
    header.insertAdjacentElement('afterend',meta);
  })();
  function renderTree(items,depth){
    var s=getState();var html='';
    items.forEach(function(item){
      var pad='padding-left:'+(12+depth*16)+'px;';
      if(item.type==='dir'){
        var dirKey='dir:'+item.path;var stored=s[dirKey];
        // Folders containing the active file always open, even if previously
        // collapsed, so navigating to a file via a link reveals it in the tree.
        var isOpen=activeAncestors[item.path]===true||stored===true;
        html+='<div class="sidebar-group" style="--depth:'+depth+';">';
        html+='<div class="sidebar-dir'+(isOpen?' open':'')+'" style="'+pad+'" data-dir="'+item.path+'">'+esc(item.name)+'</div>';
        html+='<div class="sidebar-children'+(isOpen?'':' collapsed')+'" data-dir-children="'+item.path+'">';
        html+=renderTree(item.children||[],depth+1);
        html+='</div>';
        html+='</div>';
      } else {
        var isActive=item.path===currentPath;
        html+='<a class="sidebar-file'+(isActive?' active':'')+'" href="/'+encodeURI(item.path)+'" style="'+pad+'"><span class="sidebar-icon">'+fileIcon(item.name)+'</span>'+esc(item.name)+'</a>';
      }
    });
    return html;
  }
  tree.addEventListener('click',function(e){
    var dir=e.target.closest('.sidebar-dir');if(!dir)return;
    var key='dir:'+dir.getAttribute('data-dir');
    var children=tree.querySelector('[data-dir-children="'+dir.getAttribute('data-dir')+'"]');
    if(!children)return;
    dir.classList.toggle('open');children.classList.toggle('collapsed');
    var s=getState();s[key]=dir.classList.contains('open');saveState(s);
  });
  // Drag a sidebar file out of the browser to materialize a real file
  // in Finder/Explorer (Chromium's DownloadURL convention).
  var mimeMap={md:'text/markdown',markdown:'text/markdown',html:'text/html',htm:'text/html',txt:'text/plain',log:'text/plain',json:'application/json',yaml:'application/x-yaml',yml:'application/x-yaml',xml:'application/xml',css:'text/css',js:'text/javascript',ts:'text/plain',tsx:'text/plain',jsx:'text/plain',go:'text/plain',py:'text/x-python',rb:'text/plain',rs:'text/plain',java:'text/plain',c:'text/plain',h:'text/plain',cpp:'text/plain',sh:'text/plain',csv:'text/csv',tsv:'text/tab-separated-values',toml:'text/plain',svg:'image/svg+xml',png:'image/png',jpg:'image/jpeg',jpeg:'image/jpeg',gif:'image/gif',webp:'image/webp',pdf:'application/pdf'};
  function mimeFor(name){var ext=name.split('.').pop().toLowerCase();return mimeMap[ext]||'application/octet-stream';}
  tree.addEventListener('dragstart',function(e){
    var a=e.target.closest('.sidebar-file');if(!a)return;
    var href=a.getAttribute('href')||'';
    var url;try{url=new URL(href,location.href);}catch(err){return;}
    url.searchParams.set('raw','1');
    var abs=url.toString();
    var parts=decodeURIComponent(href.replace(/^\//,'')).split('/');
    var filename=parts[parts.length-1]||'file';
    try{
      e.dataTransfer.setData('DownloadURL',mimeFor(filename)+':'+filename+':'+abs);
      e.dataTransfer.setData('text/uri-list',abs);
      e.dataTransfer.setData('text/plain',abs);
      e.dataTransfer.effectAllowed='copyMove';
    }catch(err){}
  });
  function renderSidebar(files){
    tree.innerHTML=renderTree(files,0);
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