(function() {
  var comments = [];
  var pendingSelection = null;
  var activePopover = null;
  var _fileParam = window.__servePath ? '?file=' + encodeURIComponent(window.__servePath) : '';
  function api(method, path, body) {
    var opts = { method: method, headers: { 'Content-Type': 'application/json' } };
    if (body) opts.body = JSON.stringify(body);
    var sep = (path || '').indexOf('?') >= 0 ? '&' : '?';
    var suffix = _fileParam ? (path ? sep + _fileParam.substring(1) : _fileParam) : '';
    return fetch('/api/comments' + (path || '') + suffix, opts).then(function(r) { return r.json(); });
  }
  function esc(s) { var d = document.createElement('div'); d.textContent = s; return d.innerHTML; }
  // A <mark> is phrasing content, so it cannot be a direct child of a table
  // row or a list. Goldmark puts a newline between </td> and <td>, and that
  // whitespace is a text node parented by the <tr>: wrapping it drops a <mark>
  // straight into the row, and the browser then renders an anonymous extra
  // cell — a comment spanning two columns left the row one cell wider with
  // blanks in it. These nodes still count toward the anchor match; they just
  // never get wrapped.
  var NO_MARK_PARENTS = { TABLE: 1, THEAD: 1, TBODY: 1, TFOOT: 1, TR: 1, COLGROUP: 1,
                          UL: 1, OL: 1, DL: 1, MENU: 1, SELECT: 1, OPTGROUP: 1, PICTURE: 1 };
  function canHoldMark(node) {
    var p = node.parentElement;
    return !p || !NO_MARK_PARENTS[p.tagName];
  }
  function timeAgo(iso) {
    var diff = (Date.now() - new Date(iso).getTime()) / 1000;
    if (diff < 60) return 'just now';
    if (diff < 3600) return Math.floor(diff / 60) + 'm ago';
    if (diff < 86400) return Math.floor(diff / 3600) + 'h ago';
    return Math.floor(diff / 86400) + 'd ago';
  }
  function waitForMermaid(callback) {
    if (document.querySelectorAll('pre.mermaid').length === 0) { callback(); return; }
    function allRendered() {
      var current = document.querySelectorAll('pre.mermaid');
      if (current.length === 0) return true;
      var pending = 0;
      current.forEach(function(el) { if (!el.querySelector('svg')) pending++; });
      return pending === 0;
    }
    if (allRendered()) { callback(); return; }
    var called = false; var pollId = null;
    function done() { if (called) return; called = true; if (pollId) clearInterval(pollId); callback(); }
    pollId = setInterval(function() { if (allRendered()) done(); }, 150);
    setTimeout(done, 5000);
  }
  function init() {
    api('GET', '').then(function(res) {
      comments = res.comments || [];
      waitForMermaid(function() { clearHighlights(); applyHighlights(); updateBadge(); });
    });
    setupSelectionListener();
  }
  function setupSelectionListener() {
    var btn = document.getElementById('comment-btn');
    document.addEventListener('mouseup', function(e) {
      if (e.target.closest && e.target.closest('.comment-popover, .comment-form, #comment-btn')) return;
      setTimeout(function() {
        var sel = window.getSelection();
        if (!sel || sel.isCollapsed || !sel.toString().trim()) { btn.style.display = 'none'; pendingSelection = null; return; }
        var range = sel.getRangeAt(0);
        var rect = range.getBoundingClientRect();
        var anchorText = sel.toString().trim();
        var node = range.commonAncestorContainer;
        if (node.nodeType === 3) node = node.parentElement;
        var block = node.closest('[data-source-lines]') || node.closest('p, h1, h2, h3, h4, h5, h6, li, blockquote, td, th, pre, div');
        var blockText = block ? block.textContent.trim() : '';
        var sourceLines = null;
        var slEl = block && block.closest('[data-source-lines]');
        if (slEl) {
          var parts = slEl.getAttribute('data-source-lines').split('-');
          sourceLines = { start: parseInt(parts[0], 10), end: parseInt(parts[1], 10) };
        }
        pendingSelection = { anchorText: anchorText, blockText: blockText, sourceLines: sourceLines, block: block };
        btn.style.display = 'block';
        btn.style.left = (window.scrollX + rect.left + rect.width / 2 - 30) + 'px';
        btn.style.top = (window.scrollY + rect.bottom + 6) + 'px';
      }, 10);
    });
    document.addEventListener('mousedown', function(e) {
      if (e.target.closest && e.target.closest('#comment-btn, .comment-popover, .comment-form')) return;
      btn.style.display = 'none'; closePopover();
    });
    btn.addEventListener('click', function(e) {
      e.preventDefault(); e.stopPropagation(); btn.style.display = 'none';
      if (pendingSelection) openCommentForm(pendingSelection);
    });
  }
  function openCommentForm(selInfo, parentId) {
    closePopover();
    var popover = document.createElement('div');
    popover.className = 'comment-popover';
    var block = selInfo.block;
    if (block) {
      var rect = block.getBoundingClientRect();
      popover.style.left = (window.scrollX + rect.left) + 'px';
      popover.style.top = (window.scrollY + rect.bottom + 8) + 'px';
    } else { popover.style.left = '50px'; popover.style.top = (window.scrollY + 100) + 'px'; }
    popover.innerHTML = '<div class="comment-form"><textarea placeholder="Write a comment..." autofocus></textarea><div class="hint">Ctrl+Enter to submit · Escape to cancel</div><div class="comment-form-actions"><button class="btn-cancel">Cancel</button><button class="btn-submit">Comment</button></div></div>';
    document.body.appendChild(popover); activePopover = popover;
    var ta = popover.querySelector('textarea'); ta.focus();
    ta.addEventListener('keydown', function(e) {
      if (e.key === 'Escape') { closePopover(); e.stopPropagation(); }
      if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) { submitComment(selInfo, ta.value.trim(), parentId); }
    });
    popover.querySelector('.btn-cancel').addEventListener('click', closePopover);
    popover.querySelector('.btn-submit').addEventListener('click', function() { submitComment(selInfo, ta.value.trim(), parentId); });
  }
  function submitComment(selInfo, text, parentId) {
    if (!text) return;
    var body = { text: text, anchor_text: selInfo.anchorText || '', block_text: selInfo.blockText || '' };
    if (selInfo.sourceLines) { body.source_line_start = selInfo.sourceLines.start; body.source_line_end = selInfo.sourceLines.end; }
    if (parentId) body.parent_id = parentId;
    api('POST', '', body).then(function(comment) {
      comments.push(comment); closePopover(); clearHighlights(); applyHighlights(); updateBadge();
      window.getSelection().removeAllRanges();
    });
  }
  function closePopover() { if (activePopover) { activePopover.remove(); activePopover = null; } }
  function clearHighlights() {
    document.querySelectorAll('mark.comment-highlight').forEach(function(mark) {
      var parent = mark.parentNode;
      while (mark.firstChild) parent.insertBefore(mark.firstChild, mark);
      parent.removeChild(mark); parent.normalize();
    });
  }
  function applyHighlights() {
    var roots = comments.filter(function(c) { return !c.parent_id; });
    var orphaned = [];
    roots.forEach(function(c) {
      if (!c.anchor_text) return;
      var found = highlightText(c.anchor_text, c.id, c.resolved, c.block_text, c.source_line_start, c.source_line_end);
      if (!found) orphaned.push(c);
    });
    renderOrphaned(orphaned);
  }
  // Locate the block element whose text best matches a comment's stored
  // block_text. Used to re-anchor when stored source-line numbers have gone
  // stale (e.g. the document was edited above the comment), so the highlight
  // stays on the original block instead of jumping to an earlier occurrence of
  // the same anchor text elsewhere in the document.
  function findBlockByText(blockText) {
    if (!blockText) return null;
    var norm = blockText.replace(/\s+/g, ' ').trim();
    if (!norm) return null;
    var els = document.querySelectorAll('[data-source-lines], p, li, td, th, blockquote, pre, h1, h2, h3, h4, h5, h6');
    var best = null, bestLen = Infinity;
    for (var i = 0; i < els.length; i++) {
      var el = els[i];
      if (el.closest('.comment-popover, .comment-form, .orphaned-comments')) continue;
      var t = el.textContent.replace(/\s+/g, ' ').trim();
      if (t === norm) return el; // exact (normalized) block match wins outright
      if (t.indexOf(norm) !== -1 && t.length < bestLen) { best = el; bestLen = t.length; }
    }
    return best; // smallest block containing the stored block text, or null
  }
  function highlightText(anchorText, commentId, resolved, blockText, lineStart, lineEnd) {
    // The server injects a position marker before the anchor; normally its
    // parent is the precise search root, no text search needed. But a stale
    // source-line hint can place that marker on the WRONG occurrence after the
    // document is edited. When block_text identifies a specific block and the
    // marker isn't inside it, block_text wins — it survives line shifts.
    var marker = document.querySelector('[data-comment-anchor="' + commentId + '"]');
    var blockEl = findBlockByText(blockText);
    var markerTrustworthy = marker && !(blockEl && !blockEl.contains(marker));
    var searchRoot;
    if (markerTrustworthy) {
      searchRoot = marker.parentElement;
    } else if (blockEl) {
      searchRoot = blockEl;
    } else {
      var containers = [];
      if (lineStart) {
        document.querySelectorAll('[data-source-lines]').forEach(function(el) {
          var parts = el.getAttribute('data-source-lines').split('-');
          var s = parseInt(parts[0], 10), e = parseInt(parts[1], 10);
          if (s <= lineStart && e >= lineEnd) containers.push(el);
        });
        if (containers.length > 1) {
          containers.sort(function(a, b) { return a.textContent.length - b.textContent.length; });
          var normAnchor = anchorText.replace(/\s+/g, ' ');
          var best = containers.find(function(el) {
            var t = el.textContent;
            return t.indexOf(anchorText) !== -1 || t.replace(/\s+/g, ' ').indexOf(normAnchor) !== -1;
          });
          if (best) containers = [best];
        }
      }
      searchRoot = containers.length > 0 ? containers[0] : document.querySelector('body');
    }
    var walker = document.createTreeWalker(searchRoot, NodeFilter.SHOW_TEXT);
    var textNodes = []; var fullText = '';
    var inTable = searchRoot.closest ? !!searchRoot.closest('table') : false;
    if (!inTable) inTable = !!searchRoot.querySelector('table');
    var lastCell = undefined;
    while (walker.nextNode()) {
      var n = walker.currentNode;
      if (n.parentElement.closest('mark.comment-highlight, .comment-popover, .comment-form, .orphaned-comments, script, style')) continue;
      if (inTable) {
        var cell = n.parentElement.closest('td, th');
        if (lastCell !== undefined && cell !== lastCell) fullText += '\x00';
        lastCell = cell;
      }
      textNodes.push({ node: n, start: fullText.length, wrap: canHoldMark(n) }); fullText += n.textContent;
    }
    var idx = fullText.indexOf(anchorText);
    if (idx === -1) {
      var normAnchor = anchorText.replace(/\s+/g, ' ');
      var normFull = fullText.replace(/\s+/g, ' ');
      var ni = normFull.indexOf(normAnchor);
      if (ni !== -1) {
        var nfMap = []; var fi = 0;
        for (var ci = 0; ci < normFull.length; ci++) {
          if (normFull[ci] === ' ' && /\s/.test(fullText[fi])) {
            nfMap.push(fi);
            while (fi < fullText.length && /\s/.test(fullText[fi])) fi++;
          } else { nfMap.push(fi); fi++; }
        }
        idx = nfMap[ni];
        var endOrig = (ni + normAnchor.length < nfMap.length) ? nfMap[ni + normAnchor.length] : fullText.length;
        anchorText = fullText.substring(idx, endOrig);
      }
    }
    if (idx === -1) {
      var sa = anchorText.replace(/\s+/g, '');
      if (sa.length > 0) {
        var sf = '', sfMap = [];
        for (var k = 0; k < fullText.length; k++) {
          if (!/\s/.test(fullText.charAt(k))) { sfMap.push(k); sf += fullText.charAt(k); }
        }
        var si = sf.indexOf(sa);
        if (si !== -1) { idx = sfMap[si]; anchorText = fullText.substring(sfMap[si], sfMap[si + sa.length - 1] + 1); }
      }
    }
    // If the line-hint container didn't contain the text (e.g. stored line
    // numbers became stale after frontmatter was added to the file), retry
    // from the full body before giving up.
    if (idx === -1 && searchRoot !== document.body) {
      // Prefer the block matching the stored block_text; only widen to the whole
      // body when it can't be identified. This keeps the comment on its original
      // block (and original occurrence) after edits shift line numbers, instead
      // of grabbing the first occurrence of the anchor text in the document.
      searchRoot = findBlockByText(blockText) || document.body;
      walker = document.createTreeWalker(searchRoot, NodeFilter.SHOW_TEXT);
      textNodes = []; fullText = '';
      while (walker.nextNode()) {
        var nb = walker.currentNode;
        if (nb.parentElement.closest('mark.comment-highlight, .comment-popover, .comment-form, .orphaned-comments, script, style')) continue;
        textNodes.push({ node: nb, start: fullText.length, wrap: canHoldMark(nb) }); fullText += nb.textContent;
      }
      idx = fullText.indexOf(anchorText);
      if (idx === -1) {
        var na2 = anchorText.replace(/\s+/g, ' '), nf2 = fullText.replace(/\s+/g, ' '), ni2 = nf2.indexOf(na2);
        if (ni2 !== -1) {
          var nfMap2 = [], fi2 = 0;
          for (var ci2 = 0; ci2 < nf2.length; ci2++) {
            if (nf2[ci2] === ' ' && /\s/.test(fullText[fi2])) { nfMap2.push(fi2); while (fi2 < fullText.length && /\s/.test(fullText[fi2])) fi2++; }
            else { nfMap2.push(fi2); fi2++; }
          }
          idx = nfMap2[ni2];
          anchorText = fullText.substring(idx, (ni2 + na2.length < nfMap2.length) ? nfMap2[ni2 + na2.length] : fullText.length);
        }
      }
    }
    if (idx === -1) return false;
    var remaining = anchorText.length; var pos = idx;
    for (var i = 0; i < textNodes.length && remaining > 0; i++) {
      var tn = textNodes[i]; var nodeEnd = tn.start + tn.node.textContent.length;
      if (nodeEnd <= pos) continue;
      var offsetInNode = Math.max(0, pos - tn.start);
      var charsInNode = Math.min(tn.node.textContent.length - offsetInNode, remaining);
      if (!tn.wrap) { remaining -= charsInNode; pos += charsInNode; continue; }
      var range = document.createRange();
      range.setStart(tn.node, offsetInNode); range.setEnd(tn.node, offsetInNode + charsInNode);
      var mark = document.createElement('mark');
      mark.className = 'comment-highlight' + (resolved ? ' resolved' : '');
      mark.setAttribute('data-comment-id', commentId);
      mark.addEventListener('click', (function(cid) { return function(e) { e.stopPropagation(); showCommentThread(cid, e.target); }; })(commentId));
      try { range.surroundContents(mark); } catch(ex) { var fragment = range.extractContents(); mark.appendChild(fragment); range.insertNode(mark); }
      remaining -= charsInNode; pos += charsInNode;
      if (remaining > 0) {
        walker = document.createTreeWalker(searchRoot, NodeFilter.SHOW_TEXT); textNodes = []; fullText = '';
        while (walker.nextNode()) {
          var nn = walker.currentNode;
          if (nn.parentElement.closest('mark.comment-highlight, .comment-popover, .comment-form, .orphaned-comments, script, style')) continue;
          textNodes.push({ node: nn, start: fullText.length, wrap: canHoldMark(nn) }); fullText += nn.textContent;
        }
        idx = fullText.indexOf(anchorText.substring(anchorText.length - remaining));
        if (idx === -1) break; pos = idx;
      }
    }
    return true;
  }
  function showCommentThread(commentId, targetEl) {
    closePopover();
    var root = comments.find(function(c) { return c.id === commentId; });
    if (!root) return;
    var replies = comments.filter(function(c) { return c.parent_id === commentId; });
    replies.sort(function(a, b) { return a.created_at.localeCompare(b.created_at); });
    var popover = document.createElement('div'); popover.className = 'comment-popover';
    var rect = targetEl.getBoundingClientRect();
    popover.style.left = (window.scrollX + rect.left) + 'px'; popover.style.top = (window.scrollY + rect.bottom + 8) + 'px';
    popover.innerHTML = renderThread(root, replies);
    document.body.appendChild(popover); activePopover = popover;
    popover.querySelectorAll('[data-action]').forEach(function(btn) {
      btn.addEventListener('click', function() {
        var action = btn.getAttribute('data-action'); var id = btn.getAttribute('data-id');
        if (action === 'resolve') toggleResolve(id, true);
        else if (action === 'unresolve') toggleResolve(id, false);
        else if (action === 'delete') deleteComment(id);
        else if (action === 'reply') {
          var selInfo = { anchorText: root.anchor_text, blockText: root.block_text,
            sourceLines: root.source_line_start ? { start: root.source_line_start, end: root.source_line_end } : null,
            block: targetEl.closest('[data-source-lines]') || targetEl.closest('p, h1, h2, h3, h4, h5, h6, li, blockquote, td, th, pre, div') };
          closePopover(); openCommentForm(selInfo, commentId);
        }
      });
    });
  }
  function renderThread(root, replies) {
    var cls = 'comment-thread' + (root.resolved ? ' resolved' : '');
    var html = '<div class="' + cls + '">';
    if (root.resolved) {
      html += '<div class="comment-card" style="display:flex;align-items:center;justify-content:space-between;"><span class="resolved-badge">&#10003; Resolved</span><div>' +
        '<button data-action="unresolve" data-id="' + root.id + '" class="comment-actions btn-unresolve" style="border:none;background:none;cursor:pointer;font-size:12px;">Unresolve</button>' +
        '<button data-action="delete" data-id="' + root.id + '" class="comment-actions btn-delete" style="border:none;background:none;cursor:pointer;font-size:12px;">Delete</button></div></div>';
    }
    html += '<div class="comment-card"><div class="comment-meta">' + timeAgo(root.created_at) + '</div><div class="comment-text">' + esc(root.text) + '</div><div class="comment-actions">' +
      '<button data-action="reply" data-id="' + root.id + '" class="btn-reply">Reply</button>';
    if (!root.resolved) html += '<button data-action="resolve" data-id="' + root.id + '" class="btn-resolve">Resolve</button>';
    html += '<button data-action="delete" data-id="' + root.id + '" class="btn-delete">Delete</button></div></div>';
    replies.forEach(function(r) {
      html += '<div class="comment-card reply"><div class="comment-meta">' + timeAgo(r.created_at) + '</div><div class="comment-text">' + esc(r.text) + '</div>' +
        '<div class="comment-actions"><button data-action="delete" data-id="' + r.id + '" class="btn-delete">Delete</button></div></div>';
    });
    html += '</div>'; return html;
  }
  function toggleResolve(commentId, resolved) {
    api('PATCH', '/' + commentId, { resolved: resolved }).then(function(updated) {
      var idx = comments.findIndex(function(c) { return c.id === commentId; });
      if (idx >= 0) comments[idx] = updated;
      closePopover(); clearHighlights(); applyHighlights(); updateBadge();
    });
  }
  function deleteComment(commentId) {
    api('DELETE', '/' + commentId).then(function() {
      comments = comments.filter(function(c) { return c.id !== commentId && c.parent_id !== commentId; });
      closePopover(); clearHighlights(); applyHighlights(); updateBadge();
    });
  }
  function renderOrphaned(orphaned) {
    var existing = document.querySelector('.orphaned-comments'); if (existing) existing.remove();
    if (orphaned.length === 0) return;
    var section = document.createElement('div'); section.className = 'orphaned-comments';
    section.innerHTML = '<h3>Unanchored Comments</h3>';
    orphaned.forEach(function(c) {
      var replies = comments.filter(function(r) { return r.parent_id === c.id; });
      section.innerHTML += renderThread(c, replies);
    });
    document.body.appendChild(section);
    section.querySelectorAll('[data-action]').forEach(function(btn) {
      btn.addEventListener('click', function() {
        var action = btn.getAttribute('data-action'); var id = btn.getAttribute('data-id');
        if (action === 'resolve') toggleResolve(id, true);
        else if (action === 'unresolve') toggleResolve(id, false);
        else if (action === 'delete') deleteComment(id);
      });
    });
  }
  // ---- Hide / show comments ----
  // A toggle in the comment panel header clears the inline highlights and the
  // unanchored-comments list for a distraction-free read. State persists in
  // localStorage, so it survives soft reloads and full page reloads.
  var COMMENTS_HIDDEN_KEY = 'serve-comments-hidden';
  var EYE_ICON = '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M1 12s4-7 11-7 11 7 11 7-4 7-11 7-11-7-11-7z"/><circle cx="12" cy="12" r="3"/></svg>';
  var EYE_OFF_ICON = '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19m-6.72-1.07a3 3 0 1 1-4.24-4.24"/><line x1="1" y1="1" x2="23" y2="23"/></svg>';
  function commentsHidden() { try { return localStorage.getItem(COMMENTS_HIDDEN_KEY) === '1'; } catch (e) { return false; } }
  function applyCommentsHidden() {
    var hidden = commentsHidden();
    document.body.classList.toggle('serve-comments-hidden', hidden);
    var t = document.getElementById('comment-hide-toggle');
    if (t) {
      t.innerHTML = (hidden ? EYE_OFF_ICON : EYE_ICON) + '<span>' + (hidden ? 'Show' : 'Hide') + '</span>';
      t.title = hidden ? 'Show comment highlights in the document' : 'Hide comment highlights in the document';
      t.setAttribute('aria-pressed', hidden ? 'true' : 'false');
    }
  }
  function toggleCommentsHidden() {
    try { localStorage.setItem(COMMENTS_HIDDEN_KEY, commentsHidden() ? '0' : '1'); } catch (e) {}
    applyCommentsHidden();
  }
  function updateBadge() {
    var badge = document.getElementById('comment-badge');
    var roots = comments.filter(function(c) { return !c.parent_id; });
    var unresolved = roots.filter(function(c) { return !c.resolved; });
    if (roots.length === 0) { badge.style.display = 'none'; return; }
    badge.style.display = 'block';
    badge.textContent = unresolved.length > 0 ? unresolved.length + ' comment' + (unresolved.length !== 1 ? 's' : '') : roots.length + ' resolved';
    badge.className = 'comment-count-badge' + (unresolved.length > 0 ? ' has-unresolved' : '');
  }
  function setupPanel() {
    var badge = document.getElementById('comment-badge'); var panel = document.getElementById('comment-panel'); var closeBtn = document.getElementById('panel-close');
    badge.addEventListener('click', function() { renderPanel(); panel.classList.toggle('open'); });
    closeBtn.addEventListener('click', function() { panel.classList.remove('open'); });
    // Add the hide/show toggle to the panel header (once).
    var header = panel.querySelector('.comment-panel-header');
    if (header && !document.getElementById('comment-hide-toggle')) {
      var t = document.createElement('button');
      t.id = 'comment-hide-toggle';
      t.type = 'button';
      t.className = 'comment-panel-toggle';
      t.addEventListener('click', toggleCommentsHidden);
      header.insertBefore(t, closeBtn);
    }
    applyCommentsHidden();
  }
  function renderPanel() {
    var body = document.getElementById('panel-body');
    var roots = comments.filter(function(c) { return !c.parent_id; });
    roots.sort(function(a, b) { return a.created_at.localeCompare(b.created_at); });
    if (roots.length === 0) { body.innerHTML = '<p style="color:#656d76;font-size:13px;text-align:center;padding:2em 0;">No comments yet. Select text to add one.</p>'; return; }
    var html = '';
    roots.forEach(function(c) {
      var replies = comments.filter(function(r) { return r.parent_id === c.id; });
      var anchor = c.anchor_text || '(no selection)';
      if (anchor.length > 60) anchor = anchor.substring(0, 60) + '...';
      var cls = 'panel-comment-item' + (c.resolved ? ' resolved' : '');
      var badge2 = c.resolved ? '<span class="resolved-badge" style="font-size:10px;padding:1px 6px;margin-left:6px;">resolved</span>' : '';
      html += '<div class="' + cls + '" data-panel-comment="' + c.id + '">';
      html += '<div class="panel-comment-anchor">"' + esc(anchor) + '"' + badge2 + '</div>';
      html += '<div class="panel-comment-body"><div class="comment-text">' + esc(c.text) + '</div><div class="comment-meta">' + timeAgo(c.created_at);
      if (c.source_line_start) html += ' · line ' + c.source_line_start;
      html += '</div></div>';
      if (replies.length > 0) html += '<div class="panel-comment-replies">' + replies.length + ' repl' + (replies.length === 1 ? 'y' : 'ies') + '</div>';
      html += '</div>';
    });
    body.innerHTML = html;
    body.querySelectorAll('[data-panel-comment]').forEach(function(item) {
      item.addEventListener('click', function() {
        var cid = item.getAttribute('data-panel-comment');
        var mark = document.querySelector('mark[data-comment-id="' + cid + '"]');
        if (mark) { mark.scrollIntoView({ behavior: 'smooth', block: 'center' }); setTimeout(function() { showCommentThread(cid, mark); }, 350); }
        else { showCommentThread(cid, item); }
        document.getElementById('comment-panel').classList.remove('open');
      });
    });
  }
  window.__serveOpenCommentForm = function(selInfo, parentId) { openCommentForm(selInfo, parentId); };
  window.__refreshComments = function() {
    api('GET', '').then(function(res) {
      comments = res.comments || []; clearHighlights(); applyHighlights(); updateBadge();
      var panel = document.getElementById('comment-panel');
      if (panel && panel.classList.contains('open')) renderPanel();
    });
  };
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', function() { init(); setupPanel(); });
  } else { init(); setupPanel(); }
})();