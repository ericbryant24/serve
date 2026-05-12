(function() {
  var enabled = localStorage.getItem('serve-vim-mode') === '1';
  var mode = 'normal';
  var blocks = []; var cursorIdx = -1; var selStart = -1; var selEnd = -1;
  var pendingG = false; var pendingZ = false;
  var searchQuery = ''; var searchMarks = []; var searchIdx = -1;
  var caretBlock = null; var caretNode = null; var caretOffset = 0;
  var caretAnchor = null; var caretCol = null;
  var cellTable = null; var currentCell = null;
  var indicator, toggle, searchBar, searchInput, searchCount, caretEl;
  function collectBlocks() { blocks = Array.from(document.querySelectorAll('[data-source-lines]')); }
  function setCursor(idx, scroll) {
    if (idx < 0 || idx >= blocks.length) return;
    if (cursorIdx >= 0 && cursorIdx < blocks.length) blocks[cursorIdx].classList.remove('vim-cursor');
    cursorIdx = idx; blocks[cursorIdx].classList.add('vim-cursor');
    if (scroll !== false) {
      blocks[cursorIdx].scrollIntoView({ behavior: 'smooth', block: 'nearest' });
      var rect = blocks[cursorIdx].getBoundingClientRect();
      if (rect.top < 80) window.scrollBy({ top: rect.top - 80, behavior: 'smooth' });
      if (rect.bottom > window.innerHeight - 40) window.scrollBy({ top: rect.bottom - window.innerHeight + 40, behavior: 'smooth' });
    }
    updateIndicator();
  }
  function moveCursor(delta) { var next = Math.max(0, Math.min(blocks.length - 1, cursorIdx + delta)); setCursor(next); }
  function clearVisual() { blocks.forEach(function(b) { b.classList.remove('vim-visual'); }); selStart = -1; selEnd = -1; }
  function enterVisual() { mode = 'visual'; selStart = cursorIdx; selEnd = cursorIdx; applyVisual(); updateIndicator(); }
  function extendVisual(delta) { selEnd = Math.max(0, Math.min(blocks.length-1, selEnd+delta)); setCursor(selEnd); applyVisual(); }
  function applyVisual() {
    blocks.forEach(function(b) { b.classList.remove('vim-visual'); });
    var lo = Math.min(selStart,selEnd), hi = Math.max(selStart,selEnd);
    for (var i=lo;i<=hi;i++) blocks[i].classList.add('vim-visual');
  }
  function exitVisual() { clearVisual(); mode='normal'; updateIndicator(); }
  function commentFromVisual() {
    if (selStart < 0 || selEnd < 0) return;
    var lo = Math.min(selStart,selEnd), hi = Math.max(selStart,selEnd);
    var inTable = false;
    for (var ti=lo;ti<=hi;ti++) { var tag=blocks[ti].tagName; if (tag==='TABLE'||tag==='TR'||tag==='TD'||tag==='TH'||tag==='THEAD'||tag==='TBODY'||blocks[ti].closest('table')){inTable=true;break;} }
    var anchorText, anchorBlock;
    if (inTable) { anchorBlock=blocks[lo]; var fc=anchorBlock.querySelector('td,th'); anchorText=fc?fc.textContent.trim():anchorBlock.textContent.trim(); if(fc)anchorBlock=fc; }
    else { var parts=[]; for(var i=lo;i<=hi;i++) parts.push(blocks[i].textContent.trim()); anchorText=parts.join('\n'); anchorBlock=blocks[lo]; }
    var lineStart=null, lineEnd=null;
    for (var j=lo;j<=hi;j++) { var sl=blocks[j].getAttribute('data-source-lines'); if(sl){var p=sl.split('-');var s=parseInt(p[0],10),e=parseInt(p[1],10);if(lineStart===null||s<lineStart)lineStart=s;if(lineEnd===null||e>lineEnd)lineEnd=e;} }
    var selInfo={anchorText:anchorText,blockText:anchorBlock.textContent.trim(),sourceLines:(lineStart!==null)?{start:lineStart,end:lineEnd}:null,block:anchorBlock};
    clearVisual(); mode='normal'; updateIndicator();
    if (typeof window.__serveOpenCommentForm==='function') window.__serveOpenCommentForm(selInfo);
  }
  function caretTextNodes(block) {
    var nodes=[]; var walker=document.createTreeWalker(block,NodeFilter.SHOW_TEXT,{acceptNode:function(n){if(n.parentElement&&n.parentElement.closest('.comment-popover,.comment-form,.orphaned-comments,script,style'))return NodeFilter.FILTER_REJECT;return NodeFilter.FILTER_ACCEPT;}});
    while(walker.nextNode()) nodes.push(walker.currentNode); return nodes;
  }
  function caretLinearPos(nodes,node,offset){var pos=0;for(var i=0;i<nodes.length;i++){if(nodes[i]===node)return pos+offset;pos+=nodes[i].textContent.length;}return pos;}
  function caretFromLinear(nodes,linear){if(nodes.length===0)return null;var pos=0;for(var i=0;i<nodes.length;i++){var len=nodes[i].textContent.length;if(linear<=pos+len)return{node:nodes[i],offset:linear-pos};pos+=len;}var last=nodes[nodes.length-1];return{node:last,offset:last.textContent.length};}
  function caretTotalLen(nodes){var t=0;for(var i=0;i<nodes.length;i++)t+=nodes[i].textContent.length;return t;}
  function caretCurrentRect(){if(!caretNode)return null;var range=document.createRange();var len=caretNode.textContent.length;if(len===0){try{range.selectNodeContents(caretNode.parentNode);range.collapse(true);}catch(e){return null;}}else if(caretOffset>=len){range.setStart(caretNode,len-1);range.setEnd(caretNode,len);var r=range.getBoundingClientRect();return{left:r.right,top:r.top,height:r.height};}else{range.setStart(caretNode,caretOffset);range.setEnd(caretNode,caretOffset+1);var r2=range.getBoundingClientRect();return{left:r2.left,top:r2.top,height:r2.height};}var r3=range.getBoundingClientRect();return{left:r3.left,top:r3.top,height:r3.height||16};}
  function renderCaret(){if(mode!=='caret'){caretEl.style.display='none';return;}var rect=caretCurrentRect();if(!rect){caretEl.style.display='none';return;}caretEl.style.display='block';caretEl.style.left=(rect.left+window.scrollX)+'px';caretEl.style.top=(rect.top+window.scrollY)+'px';caretEl.style.height=(rect.height||16)+'px';var vh=window.innerHeight;if(rect.top<80)window.scrollBy({top:rect.top-80,behavior:'smooth'});else if(rect.top>vh-80)window.scrollBy({top:rect.top-(vh-80),behavior:'smooth'});}
  function setCaretLinear(linear,options){var nodes=caretTextNodes(caretBlock);var total=caretTotalLen(nodes);if(linear<0)linear=0;if(linear>total)linear=total;var pos=caretFromLinear(nodes,linear);if(!pos)return;caretNode=pos.node;caretOffset=pos.offset;if(caretAnchor){var sel=window.getSelection();try{sel.setBaseAndExtent(caretAnchor.node,caretAnchor.offset,caretNode,caretOffset);}catch(e){}}else{window.getSelection().removeAllRanges();}if(!options||options.resetCol!==false){var rect=caretCurrentRect();caretCol=rect?rect.left:null;}renderCaret();}
  function caretCurrentLinear(){var nodes=caretTextNodes(caretBlock);return caretLinearPos(nodes,caretNode,caretOffset);}
  function moveCaret(delta){setCaretLinear(caretCurrentLinear()+delta);}
  function moveCaretWord(dir){var nodes=caretTextNodes(caretBlock);var text='';for(var i=0;i<nodes.length;i++)text+=nodes[i].textContent;var pos=caretCurrentLinear();var WORD=/\w/,SPACE=/\s/;if(dir>0){var n=pos;var initial=text.charAt(n);var isWord=WORD.test(initial);var isSpace=SPACE.test(initial);while(n<text.length){var c=text.charAt(n);if(isSpace){if(!SPACE.test(c))break;}else if(isWord){if(!WORD.test(c))break;}else{if(WORD.test(c)||SPACE.test(c))break;}n++;}while(n<text.length&&SPACE.test(text.charAt(n)))n++;setCaretLinear(n);}else{var m=pos;if(m>0)m--;while(m>0&&SPACE.test(text.charAt(m)))m--;var ch=text.charAt(m);var wordy=WORD.test(ch);while(m>0){var prev=text.charAt(m-1);if(wordy&&!WORD.test(prev))break;if(!wordy&&(WORD.test(prev)||SPACE.test(prev)))break;m--;}setCaretLinear(m);}}
  function moveCaretWordEnd(dir){var nodes=caretTextNodes(caretBlock);var text='';for(var i=0;i<nodes.length;i++)text+=nodes[i].textContent;var WORD=/\w/,SPACE=/\s/;var q=caretCurrentLinear();if(dir>0){while(q<text.length&&SPACE.test(text.charAt(q)))q++;if(q>=text.length){setCaretLinear(text.length);return;}var wordy=WORD.test(text.charAt(q));while(q<text.length){var c=text.charAt(q);if(SPACE.test(c))break;if(wordy?!WORD.test(c):WORD.test(c))break;q++;}setCaretLinear(q);}else{if(q===0){setCaretLinear(0);return;}q--;if(!SPACE.test(text.charAt(q))){var startWord=WORD.test(text.charAt(q));while(q>0){var prev=text.charAt(q-1);if(SPACE.test(prev))break;if(WORD.test(prev)!==startWord)break;q--;}q--;}if(q<0){setCaretLinear(0);return;}while(q>0&&SPACE.test(text.charAt(q)))q--;if(SPACE.test(text.charAt(q))){setCaretLinear(0);return;}setCaretLinear(q+1);}}
  function enterCaret(block){if(!block)return;caretBlock=block;var nodes=caretTextNodes(block);if(nodes.length===0)return;caretNode=nodes[0];caretOffset=0;caretAnchor=null;caretCol=null;mode='caret';if(cursorIdx>=0&&cursorIdx<blocks.length)blocks[cursorIdx].classList.remove('vim-cursor');window.getSelection().removeAllRanges();document.body.classList.remove('vim-mode-caret-visual');renderCaret();updateIndicator();}
  function exitCaret(returnTo){caretEl.style.display='none';document.body.classList.remove('vim-mode-caret-visual');window.getSelection().removeAllRanges();caretBlock=null;caretNode=null;caretOffset=0;caretAnchor=null;caretCol=null;mode=returnTo||'normal';if(mode==='normal'&&cursorIdx>=0&&cursorIdx<blocks.length)blocks[cursorIdx].classList.add('vim-cursor');updateIndicator();}
  function commentFromCaret(){if(!caretBlock)return;var anchorText=caretAnchor?window.getSelection().toString()||caretBlock.textContent.trim():caretBlock.textContent.trim();var selInfo={anchorText:anchorText,blockText:caretBlock.textContent.trim(),sourceLines:nearestSourceLines(caretBlock),block:caretBlock};if(cellTable){currentCell=null;cellTable=null;}exitCaret('normal');if(typeof window.__serveOpenCommentForm==='function')window.__serveOpenCommentForm(selInfo);}
  function tableCells(table){return Array.from(table.querySelectorAll('td,th'));}
  function rowCells(row){return Array.from(row.children).filter(function(c){return c.tagName==='TD'||c.tagName==='TH';});}
  function setCellCursor(cell){if(currentCell)currentCell.classList.remove('vim-cell-cursor');currentCell=cell;if(!cell)return;cell.classList.add('vim-cell-cursor');cell.scrollIntoView({behavior:'smooth',block:'nearest'});}
  function enterCell(table,hint){cellTable=table;var startCell=null;if(hint){if(hint.tagName==='TD'||hint.tagName==='TH')startCell=hint;else if(hint.tagName==='TR')startCell=hint.querySelector('td,th');else startCell=hint.querySelector('td,th');}if(!startCell)startCell=table.querySelector('td,th');if(!startCell)return false;mode='cell';if(cursorIdx>=0&&cursorIdx<blocks.length)blocks[cursorIdx].classList.remove('vim-cursor');setCellCursor(startCell);updateIndicator();return true;}
  function moveCellHoriz(delta){if(!currentCell)return;var row=currentCell.parentElement;var cells=rowCells(row);var idx=cells.indexOf(currentCell);var next=Math.max(0,Math.min(cells.length-1,idx+delta));setCellCursor(cells[next]);}
  function moveCellVert(delta){if(!currentCell)return;var row=currentCell.parentElement;var col=rowCells(row).indexOf(currentCell);var rows=Array.from(cellTable.querySelectorAll('tr'));var rowIdx=rows.indexOf(row);var next=Math.max(0,Math.min(rows.length-1,rowIdx+delta));var newCells=rowCells(rows[next]);if(!newCells.length)return;setCellCursor(newCells[Math.min(col,newCells.length-1)]);}
  function exitCell(){if(currentCell)currentCell.classList.remove('vim-cell-cursor');currentCell=null;cellTable=null;mode='normal';if(cursorIdx>=0&&cursorIdx<blocks.length)blocks[cursorIdx].classList.add('vim-cursor');updateIndicator();}
  function nearestSourceLines(el){var cur=el;while(cur&&cur!==document.body){var sl=cur.getAttribute&&cur.getAttribute('data-source-lines');if(sl){var parts=sl.split('-');return{start:parseInt(parts[0],10),end:parseInt(parts[1],10)};}cur=cur.parentElement;}return null;}
  function commentFromCell(){if(!currentCell)return;var selInfo={anchorText:currentCell.textContent.trim(),blockText:currentCell.textContent.trim(),sourceLines:nearestSourceLines(currentCell),block:currentCell};exitCell();if(typeof window.__serveOpenCommentForm==='function')window.__serveOpenCommentForm(selInfo);}
  function blockIsTableLike(block){if(!block)return false;var t=block.tagName;if(t==='TABLE'||t==='THEAD'||t==='TBODY'||t==='TR'||t==='TD'||t==='TH')return true;return !!(block.closest&&block.closest('table'));}
  function openSearch(){mode='search';searchBar.classList.add('open');searchInput.value=searchQuery;searchInput.focus();searchInput.select();updateIndicator();}
  function closeSearch(){searchBar.classList.remove('open');clearSearchMarks();mode='normal';searchInput.blur();updateIndicator();}
  function clearSearchMarks(){searchMarks.forEach(function(mark){var parent=mark.parentNode;while(mark.firstChild)parent.insertBefore(mark.firstChild,mark);parent.removeChild(mark);parent.normalize();});searchMarks=[];searchIdx=-1;searchCount.textContent='';}
  function executeSearch(query){clearSearchMarks();searchQuery=query;if(!query)return;var walker=document.createTreeWalker(document.body,NodeFilter.SHOW_TEXT);var matches=[];while(walker.nextNode()){var node=walker.currentNode;if(node.parentElement.closest('#vim-search-bar,#vim-indicator,#vim-toggle,.comment-popover,.comment-form,script,style,.orphaned-comments'))continue;var text=node.textContent;var lowerText=text.toLowerCase();var lowerQuery=query.toLowerCase();var idx=0;while((idx=lowerText.indexOf(lowerQuery,idx))!==-1){matches.push({node:node,offset:idx,length:query.length});idx+=query.length;}}if(matches.length===0){searchCount.textContent='No matches';return;}for(var i=matches.length-1;i>=0;i--){var m=matches[i];var range=document.createRange();range.setStart(m.node,m.offset);range.setEnd(m.node,m.offset+m.length);var mark=document.createElement('span');mark.className='vim-search-match';try{range.surroundContents(mark);searchMarks.unshift(mark);}catch(e){}}if(searchMarks.length>0){searchIdx=0;highlightCurrentMatch();}}
  function highlightCurrentMatch(){searchMarks.forEach(function(m,i){m.className=(i===searchIdx)?'vim-search-current':'vim-search-match';});if(searchIdx>=0&&searchIdx<searchMarks.length){searchMarks[searchIdx].scrollIntoView({behavior:'smooth',block:'center'});var el=searchMarks[searchIdx].closest('[data-source-lines]');if(el){var bi=blocks.indexOf(el);if(bi>=0)setCursor(bi,false);}searchCount.textContent=(searchIdx+1)+'/'+searchMarks.length;}}
  function nextMatch(direction){if(searchMarks.length===0){if(searchQuery){executeSearch(searchQuery);if(searchMarks.length>0)highlightCurrentMatch();}return;}searchIdx+=direction;if(searchIdx>=searchMarks.length)searchIdx=0;if(searchIdx<0)searchIdx=searchMarks.length-1;highlightCurrentMatch();}
  function nextHeading(direction){var headingTags=['H1','H2','H3','H4','H5','H6'];var i=cursorIdx+direction;while(i>=0&&i<blocks.length){if(headingTags.indexOf(blocks[i].tagName)>=0){setCursor(i);return;}i+=direction;}}
  function halfPage(direction){var vh=window.innerHeight/2;var count=0,h=0;var i=cursorIdx;while(i>=0&&i<blocks.length){h+=blocks[i].getBoundingClientRect().height;count++;if(h>=vh)break;i+=direction;}if(count<1)count=5;moveCursor(direction*count);}
  function updateIndicator(){if(!enabled){indicator.classList.remove('active','visual','search','caret','cell');return;}indicator.classList.add('active');indicator.classList.remove('visual','search','caret','cell');if(mode==='visual'){indicator.textContent='-- VISUAL --';indicator.classList.add('visual');}else if(mode==='search'){indicator.textContent='-- SEARCH --';indicator.classList.add('search');}else if(mode==='caret'){indicator.textContent=caretAnchor?'-- CARET VISUAL --':'-- CARET --';indicator.classList.add('caret');}else if(mode==='cell'){indicator.textContent='-- CELL --';indicator.classList.add('cell');}else{indicator.textContent='-- NORMAL --';}}
  function shouldHandle(e){if(!enabled)return false;var tag=document.activeElement.tagName;if(tag==='TEXTAREA'||tag==='INPUT'||tag==='SELECT')return false;if(document.activeElement.isContentEditable)return false;if(e.altKey||e.metaKey)return false;if(e.ctrlKey&&e.key!=='d'&&e.key!=='u')return false;return true;}
  function onKeyDown(e){
    if(mode==='search'&&document.activeElement===searchInput)return;
    if(!shouldHandle(e)){if(e.key==='Escape'&&!enabled){var tag=document.activeElement.tagName;if(tag!=='TEXTAREA'&&tag!=='INPUT'&&tag!=='SELECT'&&!document.activeElement.isContentEditable){toggleVim();e.preventDefault();}}return;}
    var key=e.key;
    if(e.shiftKey&&key.length===1){if(key>='a'&&key<='z')key=key.toUpperCase();else{var sm={'[':'{',']':'}','/':'?'};if(sm[key])key=sm[key];}}
    if(mode==='normal'){
      if(key==='j'){moveCursor(1);e.preventDefault();}
      else if(key==='k'){moveCursor(-1);e.preventDefault();}
      else if(key==='g'){if(pendingG){setCursor(0);pendingG=false;e.preventDefault();}else{pendingG=true;setTimeout(function(){pendingG=false;},500);e.preventDefault();}}
      else if(key==='G'){setCursor(blocks.length-1);e.preventDefault();}
      else if(key==='{'){nextHeading(-1);e.preventDefault();}
      else if(key==='}'){nextHeading(1);e.preventDefault();}
      else if(key==='d'&&e.ctrlKey){halfPage(1);e.preventDefault();}
      else if(key==='u'&&e.ctrlKey){halfPage(-1);e.preventDefault();}
      else if(key==='v'){enterVisual();e.preventDefault();}
      else if(key==='i'||key==='Enter'){if(cursorIdx>=0&&cursorIdx<blocks.length){var block=blocks[cursorIdx];if(blockIsTableLike(block)){var table=block.tagName==='TABLE'?block:block.closest('table');if(table)enterCell(table,block);}else{enterCaret(block);}e.preventDefault();}}
      else if(key==='/'){openSearch();e.preventDefault();}
      else if(key==='n'){nextMatch(1);e.preventDefault();}
      else if(key==='N'){nextMatch(-1);e.preventDefault();}
      else if(key==='H'){for(var hi=0;hi<blocks.length;hi++){var r=blocks[hi].getBoundingClientRect();if(r.top>=0){setCursor(hi);break;}}e.preventDefault();}
      else if(key==='M'){var mid=window.innerHeight/2;var bestIdx=cursorIdx,bestDist=Infinity;for(var mi=0;mi<blocks.length;mi++){var mr=blocks[mi].getBoundingClientRect();var d=Math.abs(mr.top+mr.height/2-mid);if(d<bestDist){bestDist=d;bestIdx=mi;}}setCursor(bestIdx);e.preventDefault();}
      else if(key==='L'){for(var li=blocks.length-1;li>=0;li--){var lr=blocks[li].getBoundingClientRect();if(lr.bottom<=window.innerHeight){setCursor(li);break;}}e.preventDefault();}
      else if(key==='z'){if(pendingZ){if(cursorIdx>=0&&cursorIdx<blocks.length)blocks[cursorIdx].scrollIntoView({behavior:'smooth',block:'center'});pendingZ=false;}else{pendingZ=true;setTimeout(function(){pendingZ=false;},500);}e.preventDefault();}
      else if(key==='Escape'){if(searchQuery){clearSearchMarks();searchQuery='';}else{toggleVim();}e.preventDefault();}
      return;
    }
    if(mode==='visual'){
      if(key==='j'){extendVisual(1);e.preventDefault();}
      else if(key==='k'){extendVisual(-1);e.preventDefault();}
      else if(key==='G'){selEnd=blocks.length-1;setCursor(selEnd);applyVisual();e.preventDefault();}
      else if(key==='g'){if(pendingG){selEnd=0;setCursor(0);applyVisual();pendingG=false;e.preventDefault();}else{pendingG=true;setTimeout(function(){pendingG=false;},500);e.preventDefault();}}
      else if(key==='c'){commentFromVisual();e.preventDefault();}
      else if(key==='Escape'||key==='v'){exitVisual();e.preventDefault();}
      return;
    }
    if(mode==='cell'){
      if(key==='h'){moveCellHoriz(-1);e.preventDefault();}
      else if(key==='l'){moveCellHoriz(1);e.preventDefault();}
      else if(key==='j'){moveCellVert(1);e.preventDefault();}
      else if(key==='k'){moveCellVert(-1);e.preventDefault();}
      else if(key==='i'||key==='Enter'){if(currentCell){var savedCell=currentCell;currentCell.classList.remove('vim-cell-cursor');enterCaret(savedCell);}e.preventDefault();}
      else if(key==='c'){commentFromCell();e.preventDefault();}
      else if(key==='Escape'){exitCell();e.preventDefault();}
      return;
    }
    if(mode==='caret'){
      if(key==='h'){moveCaret(-1);e.preventDefault();}
      else if(key==='l'){moveCaret(1);e.preventDefault();}
      else if(key==='w'){moveCaretWord(1);e.preventDefault();}
      else if(key==='b'){moveCaretWord(-1);e.preventDefault();}
      else if(key==='e'){if(pendingG){moveCaretWordEnd(-1);pendingG=false;}else{moveCaretWordEnd(1);}e.preventDefault();}
      else if(key==='g'){if(pendingG){setCaretLinear(0);pendingG=false;}else{pendingG=true;setTimeout(function(){pendingG=false;},500);}e.preventDefault();}
      else if(key==='G'){var nodesG=caretTextNodes(caretBlock);setCaretLinear(caretTotalLen(nodesG));e.preventDefault();}
      else if(key==='v'){if(caretAnchor){caretAnchor=null;document.body.classList.remove('vim-mode-caret-visual');window.getSelection().removeAllRanges();updateIndicator();}else{caretAnchor={node:caretNode,offset:caretOffset};document.body.classList.add('vim-mode-caret-visual');var sel=window.getSelection();try{sel.setBaseAndExtent(caretAnchor.node,caretAnchor.offset,caretNode,caretOffset);}catch(e2){}updateIndicator();}e.preventDefault();}
      else if(key==='c'){commentFromCaret();e.preventDefault();}
      else if(key==='Escape'){if(caretAnchor){caretAnchor=null;document.body.classList.remove('vim-mode-caret-visual');window.getSelection().removeAllRanges();updateIndicator();}else if(cellTable){var savedTable=cellTable;var savedCell2=caretBlock;exitCaret('cell');setCellCursor(savedCell2);}else{exitCaret('normal');}e.preventDefault();}
      return;
    }
  }
  function onSearchKeyDown(e){if(e.key==='Enter'){var q=searchInput.value;searchBar.classList.remove('open');mode='normal';searchInput.blur();executeSearch(q);updateIndicator();e.preventDefault();}else if(e.key==='Escape'){closeSearch();e.preventDefault();}}
  function toggleVim(){enabled=!enabled;localStorage.setItem('serve-vim-mode',enabled?'1':'0');toggle.classList.toggle('on',enabled);if(enabled){collectBlocks();if(blocks.length>0&&cursorIdx<0)setCursor(0,false);updateIndicator();}else{if(cursorIdx>=0&&cursorIdx<blocks.length)blocks[cursorIdx].classList.remove('vim-cursor');cursorIdx=-1;clearVisual();closeSearch();if(caretEl)caretEl.style.display='none';document.body.classList.remove('vim-mode-caret-visual');window.getSelection().removeAllRanges();if(currentCell)currentCell.classList.remove('vim-cell-cursor');caretBlock=null;caretNode=null;caretOffset=0;caretAnchor=null;caretCol=null;currentCell=null;cellTable=null;mode='normal';indicator.classList.remove('active');}}
  function initVim(){
    indicator=document.createElement('div');indicator.id='vim-indicator';indicator.textContent='-- NORMAL --';document.body.appendChild(indicator);
    toggle=document.createElement('button');toggle.id='vim-toggle';toggle.textContent='Vi';toggle.title='Toggle vim mode (Escape)';toggle.addEventListener('click',function(e){e.preventDefault();e.stopPropagation();toggleVim();});document.body.appendChild(toggle);
    searchBar=document.createElement('div');searchBar.id='vim-search-bar';searchBar.innerHTML='<span class="prompt">/</span><input type="text" autocomplete="off" spellcheck="false"><span class="count"></span>';document.body.appendChild(searchBar);searchInput=searchBar.querySelector('input');searchCount=searchBar.querySelector('.count');searchInput.addEventListener('keydown',onSearchKeyDown);
    caretEl=document.createElement('div');caretEl.id='vim-caret';document.body.appendChild(caretEl);
    document.addEventListener('keydown',onKeyDown);
    window.addEventListener('scroll',function(){if(mode==='caret')renderCaret();},true);
    window.addEventListener('resize',function(){if(mode==='caret')renderCaret();});
    document.addEventListener('click',function(e){if(!enabled)return;if(e.target.closest('#vim-toggle,#vim-search-bar,.comment-popover,.comment-form,#comment-btn,.comment-count-badge,.comment-panel'))return;if(mode==='caret'){if(cellTable){currentCell=null;cellTable=null;}exitCaret('normal');}else if(mode==='cell'){exitCell();}var block=e.target.closest('[data-source-lines]');if(!block)return;var idx=blocks.indexOf(block);if(idx>=0)setCursor(idx,false);});
    if(enabled){toggle.classList.add('on');collectBlocks();if(blocks.length>0)setCursor(0,false);updateIndicator();}
    var observer=new MutationObserver(function(){var oldLen=blocks.length;collectBlocks();if(enabled&&blocks.length!==oldLen&&cursorIdx>=blocks.length)cursorIdx=blocks.length-1;});
    observer.observe(document.body,{childList:true,subtree:true});
  }
  if(document.readyState==='loading'){document.addEventListener('DOMContentLoaded',initVim);}else{initVim();}
})();