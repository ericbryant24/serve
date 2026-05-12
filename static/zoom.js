(function() {
  var WIDTH_KEY='serve-zoom',FONT_KEY='serve-font-zoom';
  var WIDTH_STEPS=[{label:'48',value:'48em'},{label:'60',value:'60em'},{label:'72',value:'72em'},{label:'90',value:'90em'},{label:'∞',value:'none'}];
  var FONT_STEPS=[80,90,100,110,125,150];
  var DEFAULT_WIDTH=0,DEFAULT_FONT=2;
  function loadInt(key,def,max){try{var v=parseInt(localStorage.getItem(key),10);return(v>=0&&v<max)?v:def;}catch(e){return def;}}
  function save(key,v){try{localStorage.setItem(key,String(v));}catch(e){}}
  function applyWidth(step){document.body.style.maxWidth=WIDTH_STEPS[step].value;}
  function applyFont(step){document.body.style.fontSize=FONT_STEPS[step]+'%';}
  var wrap=document.createElement('div');wrap.id='zoom-control';
  var panel=document.createElement('div');panel.id='zoom-panel';
  var fontRow=document.createElement('div');fontRow.className='zoom-row';
  var fontLbl=document.createElement('span');fontLbl.className='zoom-row-label';fontLbl.textContent='Zoom';
  var fontSlider=document.createElement('input');fontSlider.type='range';fontSlider.min='0';fontSlider.max=String(FONT_STEPS.length-1);fontSlider.step='1';
  var fontVal=document.createElement('span');fontVal.className='zoom-row-value';
  fontRow.appendChild(fontLbl);fontRow.appendChild(fontSlider);fontRow.appendChild(fontVal);
  var widthRow=document.createElement('div');widthRow.className='zoom-row';
  var widthLbl=document.createElement('span');widthLbl.className='zoom-row-label';widthLbl.textContent='Width';
  var widthSlider=document.createElement('input');widthSlider.type='range';widthSlider.min='0';widthSlider.max=String(WIDTH_STEPS.length-1);widthSlider.step='1';
  var widthVal=document.createElement('span');widthVal.className='zoom-row-value';
  widthRow.appendChild(widthLbl);widthRow.appendChild(widthSlider);widthRow.appendChild(widthVal);
  panel.appendChild(fontRow);panel.appendChild(widthRow);
  var btn=document.createElement('button');btn.id='zoom-btn';btn.title='Adjust zoom and width';btn.innerHTML='🔍';
  wrap.appendChild(panel);wrap.appendChild(btn);document.body.appendChild(wrap);
  var curWidth=loadInt(WIDTH_KEY,DEFAULT_WIDTH,WIDTH_STEPS.length);
  var curFont=loadInt(FONT_KEY,DEFAULT_FONT,FONT_STEPS.length);
  widthSlider.value=String(curWidth);widthVal.textContent=WIDTH_STEPS[curWidth].label;applyWidth(curWidth);
  fontSlider.value=String(curFont);fontVal.textContent=FONT_STEPS[curFont]+'%';applyFont(curFont);
  btn.addEventListener('click',function(e){e.stopPropagation();panel.classList.toggle('open');});
  widthSlider.addEventListener('input',function(){var step=parseInt(widthSlider.value,10);widthVal.textContent=WIDTH_STEPS[step].label;applyWidth(step);save(WIDTH_KEY,step);});
  fontSlider.addEventListener('input',function(){var step=parseInt(fontSlider.value,10);fontVal.textContent=FONT_STEPS[step]+'%';applyFont(step);save(FONT_KEY,step);});
  document.addEventListener('click',function(e){if(!wrap.contains(e.target))panel.classList.remove('open');});
})();