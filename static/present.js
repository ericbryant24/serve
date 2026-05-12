(function() {
  var btn=document.createElement('button');btn.id='present-btn';btn.title='Open slide presentation in a new tab';
  btn.innerHTML='<span style="font-size:13px">▶</span><span>Present</span>';
  btn.addEventListener('click',function(e){e.preventDefault();e.stopPropagation();window.open(location.pathname+'?present=1','_blank');});
  if(document.body){document.body.appendChild(btn);}else{document.addEventListener('DOMContentLoaded',function(){document.body.appendChild(btn);});}
})();