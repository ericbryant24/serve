package main

import (
	"crypto/md5"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// ---------------------------------------------------------------------------
// Favicon generation
// ---------------------------------------------------------------------------

var faviconEmojis = []string{
	"📘", "📕", "📗", "📙", "📓",
	"📝", "📋", "📄", "📃", "📜",
	"🗞", "📰", "📚", "📖", "📔",
	"🔬", "🧪", "🧬", "🔭", "💡",
	"🎨", "🌈", "🔥", "💧", "🌱",
	"🚀", "🛸", "🌍", "🌊", "🌋",
	"🎲", "🎯", "🎰", "🎳", "🎮",
	"🦁", "🦅", "🦉", "🐙", "🦋",
}

var faviconColors = []string{
	"#264653", "#2a9d8f", "#e9c46a", "#f4a261", "#e76f51",
	"#606c38", "#283618", "#dda15e", "#bc6c25", "#6d6875",
	"#b5838d", "#e5989b", "#ffb4a2", "#457b9d", "#1d3557",
	"#a8dadc", "#2b2d42", "#8d99ae", "#ef233c", "#d90429",
}

func faviconForPath(path string) (string, string) {
	h := md5.Sum([]byte(path))
	n := 0
	for _, b := range h {
		n = n*256 + int(b)
	}
	if n < 0 {
		n = -n
	}
	emoji := faviconEmojis[n%len(faviconEmojis)]
	color := faviconColors[(n>>8)%len(faviconColors)]
	return emoji, color
}

func faviconLink(path string) string {
	emoji, bg := faviconForPath(path)
	svg := fmt.Sprintf(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100"><rect width="100" height="100" rx="20" fill="%s"/><text x="50" y="72" font-size="60" text-anchor="middle">%s</text></svg>`,
		bg, emoji,
	)
	b64 := base64.StdEncoding.EncodeToString([]byte(svg))
	return fmt.Sprintf("  <link rel=\"icon\" href=\"data:image/svg+xml;base64,%s\">\n", b64)
}

// ---------------------------------------------------------------------------
// Reload script (soft reload — no full-page flash)
// ---------------------------------------------------------------------------

const reloadScript = `(function() {
  function connect() {
    var ws = new WebSocket('ws://' + location.host + '/ws');
    ws.onmessage = function(event) {
      var data = JSON.parse(event.data);
      if (data.type === 'reload') { softReload(); }
      else if (data.type === 'comments-updated') {
        if (window.__refreshComments) window.__refreshComments();
      } else if (data.type === 'filetree') {
        if (window.__updateSidebarTree) window.__updateSidebarTree(data.files);
      }
    };
    ws.onclose = function() { setTimeout(connect, 1000); };
  }
  function softReload() {
    var sc = document.getElementById('serve-content');
    if (!sc) { location.reload(); return; }
    var scrollY = window.scrollY;
    fetch(location.href, {cache: 'no-store'})
      .then(function(r) { return r.text(); })
      .then(function(html) {
        try {
          var doc = new DOMParser().parseFromString(html, 'text/html');
          var nc = doc.getElementById('serve-content');
          if (!nc) { location.reload(); return; }
          sc.innerHTML = nc.innerHTML;
          window.scrollTo(0, scrollY);
          if (window.mermaid) {
            var mels = sc.querySelectorAll('pre.mermaid:not([data-processed])');
            if (mels.length) mermaid.run({nodes: Array.from(mels)});
          }
          if (window.__refreshComments) window.__refreshComments();
        } catch(e) { location.reload(); }
      })
      .catch(function() { location.reload(); });
  }
  connect();
})();`

// ---------------------------------------------------------------------------
// Comment CSS / JS / HTML
// ---------------------------------------------------------------------------

const commentCSS = `
    mark.comment-highlight {
      background: rgba(255, 213, 79, 0.35);
      cursor: pointer;
      border-radius: 2px;
      transition: background 0.15s;
    }
    mark.comment-highlight:hover { background: rgba(255, 213, 79, 0.55); }
    mark.comment-highlight.resolved { background: rgba(76, 175, 80, 0.18); }
    mark.comment-highlight.resolved:hover { background: rgba(76, 175, 80, 0.35); }
    #comment-btn {
      position: absolute;
      background: #0078d4;
      color: #fff;
      border: none;
      border-radius: 6px;
      padding: 5px 12px;
      font-size: 13px;
      font-weight: 500;
      cursor: pointer;
      box-shadow: 0 2px 8px rgba(0,0,0,0.18);
      z-index: 1000;
      user-select: none;
    }
    #comment-btn:hover { background: #106ebe; }
    .comment-popover {
      position: absolute;
      background: #fff;
      border: 1px solid #d0d7de;
      border-radius: 8px;
      box-shadow: 0 4px 16px rgba(0,0,0,0.12);
      z-index: 999;
      width: 360px;
      max-height: 480px;
      overflow-y: auto;
      padding: 0;
    }
    .comment-thread { border-left: 3px solid #0078d4; margin: 8px; border-radius: 4px; background: #f8f9fa; }
    .comment-thread.resolved { border-left-color: #22c55e; opacity: 0.75; }
    .comment-thread.resolved:hover { opacity: 1; }
    .comment-card { padding: 10px 14px; }
    .comment-card.reply { padding-left: 28px; border-top: 1px solid #e8e8e8; background: #fdfdfe; }
    .comment-meta { font-size: 11px; color: #656d76; margin-bottom: 4px; }
    .comment-text { font-size: 13px; line-height: 1.5; white-space: pre-wrap; word-break: break-word; }
    .comment-actions { margin-top: 6px; display: flex; gap: 6px; }
    .comment-actions button { background: none; border: 1px solid transparent; border-radius: 4px; padding: 2px 8px; font-size: 12px; cursor: pointer; }
    .comment-actions .btn-reply { color: #0078d4; }
    .comment-actions .btn-reply:hover { background: #e8f0fe; }
    .comment-actions .btn-resolve { color: #22c55e; }
    .comment-actions .btn-resolve:hover { background: #f0fdf0; }
    .comment-actions .btn-unresolve { color: #656d76; }
    .comment-actions .btn-unresolve:hover { background: #f0f0f0; }
    .comment-actions .btn-delete { color: #d73a49; }
    .comment-actions .btn-delete:hover { background: #fff0f0; }
    .resolved-badge { display: inline-block; background: #f0fdf0; color: #22c55e; font-size: 12px; font-weight: 600; padding: 2px 8px; border-radius: 4px; }
    .comment-form { padding: 10px 14px; }
    .comment-form textarea { width: 100%; min-height: 60px; border: 1px solid #d0d7de; border-radius: 6px; padding: 8px; font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Helvetica, Arial, sans-serif; font-size: 13px; resize: vertical; box-sizing: border-box; }
    .comment-form textarea:focus { outline: none; border-color: #0078d4; box-shadow: 0 0 0 2px rgba(0,120,212,0.2); }
    .comment-form .hint { font-size: 11px; color: #656d76; margin-top: 4px; }
    .comment-form-actions { display: flex; gap: 8px; margin-top: 8px; justify-content: flex-end; }
    .comment-form-actions button { border: none; border-radius: 6px; padding: 5px 14px; font-size: 13px; cursor: pointer; }
    .comment-form-actions .btn-cancel { background: #f0f0f0; color: #24292e; }
    .comment-form-actions .btn-cancel:hover { background: #e0e0e0; }
    .comment-form-actions .btn-submit { background: #0078d4; color: #fff; }
    .comment-form-actions .btn-submit:hover { background: #106ebe; }
    .orphaned-comments { margin-top: 2em; padding: 1em; border: 1px dashed #d0d7de; border-radius: 8px; background: #fffbeb; }
    .orphaned-comments h3 { margin-top: 0; font-size: 14px; color: #9a6700; }
    .comment-count-badge { position: fixed; bottom: 20px; right: 20px; background: #0078d4; color: #fff; border-radius: 20px; padding: 8px 16px; font-size: 13px; font-weight: 500; cursor: pointer; box-shadow: 0 2px 8px rgba(0,0,0,0.18); z-index: 1001; user-select: none; }
    .comment-count-badge:hover { background: #106ebe; }
    .comment-count-badge.has-unresolved { background: #e36209; }
    .comment-panel { position: fixed; top: 0; right: 0; width: 380px; height: 100vh; background: #fff; border-left: 1px solid #d0d7de; box-shadow: -4px 0 16px rgba(0,0,0,0.08); z-index: 1000; overflow-y: auto; transform: translateX(100%); transition: transform 0.25s ease; }
    .comment-panel.open { transform: translateX(0); }
    .comment-panel-header { position: sticky; top: 0; background: #fff; padding: 16px 20px; border-bottom: 1px solid #e8e8e8; display: flex; align-items: center; justify-content: space-between; z-index: 1; }
    .comment-panel-header h3 { margin: 0; font-size: 15px; font-weight: 600; }
    .comment-panel-close { background: none; border: none; font-size: 20px; cursor: pointer; color: #656d76; padding: 0 4px; line-height: 1; }
    .comment-panel-close:hover { color: #24292e; }
    .comment-panel-body { padding: 12px; }
    .panel-comment-item { border: 1px solid #e8e8e8; border-radius: 8px; margin-bottom: 10px; overflow: hidden; cursor: pointer; transition: border-color 0.15s; }
    .panel-comment-item:hover { border-color: #0078d4; }
    .panel-comment-item.resolved { opacity: 0.65; }
    .panel-comment-item.resolved:hover { opacity: 1; }
    .panel-comment-anchor { background: #fffbeb; padding: 8px 12px; font-size: 12px; color: #9a6700; border-bottom: 1px solid #e8e8e8; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
    .panel-comment-item.resolved .panel-comment-anchor { background: #f0fdf0; color: #22c55e; }
    .panel-comment-body { padding: 10px 12px; }
    .panel-comment-body .comment-text { font-size: 13px; margin-bottom: 4px; }
    .panel-comment-body .comment-meta { font-size: 11px; color: #656d76; }
    .panel-comment-replies { font-size: 11px; color: #656d76; padding: 0 12px 8px; }`

const commentJS = `(function() {
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
      waitForMermaid(function() { applyHighlights(); updateBadge(); });
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
  function highlightText(anchorText, commentId, resolved, blockText, lineStart, lineEnd) {
    var containers = [];
    if (lineStart) {
      document.querySelectorAll('[data-source-lines]').forEach(function(el) {
        var parts = el.getAttribute('data-source-lines').split('-');
        var s = parseInt(parts[0], 10), e = parseInt(parts[1], 10);
        if (s <= lineStart && e >= lineEnd) containers.push(el);
      });
      if (containers.length > 1) containers.sort(function(a, b) { return a.textContent.length - b.textContent.length; });
    }
    var searchRoot = containers.length > 0 ? containers[0] : document.querySelector('body');
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
      textNodes.push({ node: n, start: fullText.length }); fullText += n.textContent;
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
    if (idx === -1) return false;
    var remaining = anchorText.length; var pos = idx;
    for (var i = 0; i < textNodes.length && remaining > 0; i++) {
      var tn = textNodes[i]; var nodeEnd = tn.start + tn.node.textContent.length;
      if (nodeEnd <= pos) continue;
      var offsetInNode = Math.max(0, pos - tn.start);
      var charsInNode = Math.min(tn.node.textContent.length - offsetInNode, remaining);
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
          textNodes.push({ node: nn, start: fullText.length }); fullText += nn.textContent;
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
})();`

const commentHTML = `<button id="comment-btn" style="display:none">Comment</button>
<div id="comment-badge" class="comment-count-badge" style="display:none"></div>
<div id="comment-panel" class="comment-panel">
  <div class="comment-panel-header">
    <h3>Comments</h3>
    <button class="comment-panel-close" id="panel-close">&times;</button>
  </div>
  <div class="comment-panel-body" id="panel-body"></div>
</div>`

// ---------------------------------------------------------------------------
// Vim mode CSS / JS
// ---------------------------------------------------------------------------

const vimCSS = `
    .vim-cursor { outline: 2px solid #3b82f6; outline-offset: 2px; border-radius: 3px; transition: outline-color 0.15s; }
    .vim-visual { background: rgba(59, 130, 246, 0.12) !important; outline: 2px solid rgba(59, 130, 246, 0.4); outline-offset: 1px; border-radius: 3px; }
    #vim-indicator { position: fixed; bottom: 8px; left: 12px; font-family: "SFMono-Regular", Consolas, "Liberation Mono", Menlo, monospace; font-size: 12px; font-weight: 600; padding: 3px 10px; border-radius: 4px; z-index: 10000; pointer-events: none; opacity: 0; transition: opacity 0.15s; color: #e2e8f0; background: #1e293b; letter-spacing: 0.5px; }
    #vim-indicator.active { opacity: 1; }
    #vim-indicator.visual { background: #1e40af; }
    #vim-indicator.search { background: #854d0e; }
    #vim-toggle { position: fixed; bottom: 62px; right: 20px; width: 32px; height: 32px; border-radius: 50%; border: 1px solid #d1d5db; background: #fff; color: #6b7280; font-family: "SFMono-Regular", Consolas, "Liberation Mono", Menlo, monospace; font-size: 11px; font-weight: 700; cursor: pointer; z-index: 9999; display: flex; align-items: center; justify-content: center; box-shadow: 0 1px 3px rgba(0,0,0,0.1); transition: all 0.15s; }
    #vim-toggle:hover { border-color: #3b82f6; color: #3b82f6; }
    #vim-toggle.on { background: #1e293b; color: #e2e8f0; border-color: #1e293b; }
    #vim-search-bar { position: fixed; bottom: 0; left: 0; right: 0; background: #1e293b; color: #e2e8f0; font-family: "SFMono-Regular", Consolas, "Liberation Mono", Menlo, monospace; font-size: 14px; padding: 6px 12px; display: none; z-index: 10001; align-items: center; }
    #vim-search-bar.open { display: flex; }
    #vim-search-bar .prompt { color: #94a3b8; margin-right: 4px; }
    #vim-search-bar input { flex: 1; background: transparent; border: none; color: #e2e8f0; font: inherit; outline: none; }
    #vim-search-bar .count { color: #94a3b8; margin-left: 12px; font-size: 12px; }
    .vim-search-match { background: rgba(234, 179, 8, 0.3) !important; border-radius: 2px; }
    .vim-search-current { background: rgba(234, 179, 8, 0.6) !important; outline: 2px solid #eab308; outline-offset: 1px; border-radius: 2px; }
    .vim-cell-cursor { outline: 2px solid #3b82f6; outline-offset: -2px; background: rgba(59, 130, 246, 0.06) !important; }
    #vim-caret { position: absolute; width: 2px; background: #3b82f6; z-index: 9998; pointer-events: none; display: none; animation: vim-caret-blink 1s steps(2, start) infinite; }
    @keyframes vim-caret-blink { to { visibility: hidden; } }
    #vim-indicator.caret { background: #047857; }
    #vim-indicator.cell { background: #6d28d9; }
    .vim-mode-caret-visual ::selection { background: rgba(59, 130, 246, 0.35); }`

const vimJS = `(function() {
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
})();`

// ---------------------------------------------------------------------------
// Zoom CSS / JS
// ---------------------------------------------------------------------------

const zoomCSS = `
    #zoom-control { position: fixed; bottom: 100px; right: 20px; z-index: 9999; display: flex; flex-direction: column; align-items: center; gap: 4px; }
    #zoom-btn { width: 32px; height: 32px; border-radius: 50%; border: 1px solid #d1d5db; background: #fff; color: #6b7280; font-size: 15px; cursor: pointer; display: flex; align-items: center; justify-content: center; box-shadow: 0 1px 3px rgba(0,0,0,0.1); transition: all 0.15s; }
    #zoom-btn:hover { border-color: #3b82f6; color: #3b82f6; }
    #zoom-panel { display: none; background: #fff; border: 1px solid #d0d7de; border-radius: 8px; padding: 12px 14px; box-shadow: 0 4px 12px rgba(0,0,0,0.12); flex-direction: column; gap: 10px; width: 180px; position: absolute; bottom: 40px; right: 0; }
    #zoom-panel.open { display: flex; }
    .zoom-row { display: flex; align-items: center; gap: 8px; }
    .zoom-row-label { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Helvetica, Arial, sans-serif; font-size: 11px; font-weight: 600; color: #24292e; width: 40px; flex-shrink: 0; }
    .zoom-row input[type="range"] { flex: 1; height: 4px; accent-color: #3b82f6; cursor: pointer; }
    .zoom-row-value { font-family: "SFMono-Regular", Consolas, "Liberation Mono", Menlo, monospace; font-size: 10px; color: #656d76; width: 28px; text-align: right; flex-shrink: 0; }`

const zoomJS = `(function() {
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
})();`

// ---------------------------------------------------------------------------
// Present button (marp)
// ---------------------------------------------------------------------------

const presentCSS = `
    #present-btn { position: fixed; bottom: 140px; right: 20px; height: 32px; padding: 0 14px; border-radius: 16px; border: 1px solid #d1d5db; background: #fff; color: #374151; font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Helvetica, Arial, sans-serif; font-size: 12px; font-weight: 600; cursor: pointer; z-index: 9999; display: inline-flex; align-items: center; gap: 6px; box-shadow: 0 1px 3px rgba(0,0,0,0.1); transition: all 0.15s; }
    #present-btn:hover { border-color: #3b82f6; color: #3b82f6; }`

const presentJS = `(function() {
  var btn=document.createElement('button');btn.id='present-btn';btn.title='Open slide presentation in a new tab';
  btn.innerHTML='<span style="font-size:13px">▶</span><span>Present</span>';
  btn.addEventListener('click',function(e){e.preventDefault();e.stopPropagation();window.open(location.pathname+'?present=1','_blank');});
  if(document.body){document.body.appendChild(btn);}else{document.addEventListener('DOMContentLoaded',function(){document.body.appendChild(btn);});}
})();`

// ---------------------------------------------------------------------------
// Sidebar CSS / JS
// ---------------------------------------------------------------------------

const sidebarCSS = `
    #serve-sidebar { position: fixed; top: 0; left: 0; width: 260px; height: 100vh; background: #f6f8fa; border-right: 1px solid #d0d7de; overflow-y: auto; z-index: 900; font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Helvetica, Arial, sans-serif; font-size: 13px; transform: translateX(0); transition: transform 0.2s ease; }
    #serve-sidebar.collapsed { transform: translateX(-260px); }
    #serve-sidebar-header { position: sticky; top: 0; background: #f6f8fa; padding: 14px 16px; border-bottom: 1px solid #d0d7de; font-weight: 600; font-size: 14px; color: #24292e; display: flex; align-items: center; justify-content: space-between; z-index: 1; }
    #serve-sidebar-header .dir-name { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
    #serve-sidebar-toggle { position: fixed; top: 10px; left: 268px; z-index: 901; background: #f6f8fa; border: 1px solid #d0d7de; border-radius: 6px; width: 28px; height: 28px; display: flex; align-items: center; justify-content: center; cursor: pointer; font-size: 16px; color: #656d76; transition: left 0.2s ease; line-height: 1; }
    #serve-sidebar-toggle:hover { background: #e8e8e8; }
    #serve-sidebar-toggle.collapsed { left: 8px; }
    #serve-sidebar-tree { padding: 8px 0; }
    .sidebar-dir, .sidebar-file { display: block; padding: 3px 12px 3px 0; color: #24292e; text-decoration: none; cursor: pointer; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; border-radius: 4px; margin: 0 6px; }
    .sidebar-dir { font-weight: 500; user-select: none; }
    .sidebar-dir:hover, .sidebar-file:hover { background: #e8e8e8; }
    .sidebar-file.active { background: #ddf4ff; color: #0366d6; font-weight: 500; }
    .sidebar-dir::before { content: '\25B6'; display: inline-block; font-size: 9px; margin-right: 4px; transition: transform 0.15s; }
    .sidebar-dir.open::before { transform: rotate(90deg); }
    .sidebar-children.collapsed { display: none; }
    .sidebar-icon { display: inline-block; width: 16px; text-align: center; margin-right: 4px; font-size: 12px; }
    body.has-sidebar { margin-left: 280px !important; }
    body.sidebar-collapsed { margin-left: 20px !important; }`

const sidebarJS = `(function() {
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
})();`

// ---------------------------------------------------------------------------
// HTML templates
// ---------------------------------------------------------------------------

const headTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>%s</title>
%s
  <style>
    body {
      max-width: 48em;
      margin: 2em auto;
      padding: 0 1em;
      font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Helvetica, Arial, sans-serif;
      line-height: 1.6;
      color: #24292e;
      background: #fff;
    }
    h1, h2, h3, h4, h5, h6 { margin-top: 1.5em; margin-bottom: 0.5em; font-weight: 600; line-height: 1.25; }
    h1 { font-size: 2em; border-bottom: 1px solid #eaecef; padding-bottom: 0.3em; }
    h2 { font-size: 1.5em; border-bottom: 1px solid #eaecef; padding-bottom: 0.3em; }
    a { color: #0366d6; text-decoration: none; }
    a:hover { text-decoration: underline; }
    pre { background: #f6f8fa; padding: 1em; overflow-x: auto; border-radius: 6px; line-height: 1.45; }
    code { font-family: "SFMono-Regular", Consolas, "Liberation Mono", Menlo, monospace; font-size: 0.875em; }
    :not(pre) > code { background: #f6f8fa; padding: 0.2em 0.4em; border-radius: 3px; }
    img { max-width: 100%%; height: auto; }
    table { border-collapse: collapse; width: 100%%; margin: 1em 0; }
    th, td { border: 1px solid #dfe2e5; padding: 0.5em 0.75em; text-align: left; }
    th { background: #f6f8fa; font-weight: 600; }
    tr:nth-child(2n) { background: #f6f8fa; }
    blockquote { border-left: 4px solid #dfe2e5; margin: 0; padding: 0 1em; color: #6a737d; }
    hr { border: none; border-top: 1px solid #eaecef; margin: 1.5em 0; }
    .highlight { background: #f6f8fa; border-radius: 6px; }
    .highlight pre { background: transparent; margin: 0; }
    %s
`

const headClose = `  </style>
  <script type="module">
    import mermaid from 'https://cdn.jsdelivr.net/npm/mermaid@11/dist/mermaid.esm.min.mjs';
    mermaid.initialize({ startOnLoad: true, theme: 'default' });
  </script>
</head>
<body>
`

const bodyClose = `</body>
</html>`

// ---------------------------------------------------------------------------
// HTML escaping helpers
// ---------------------------------------------------------------------------

func htmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, `"`, "&quot;")
	return s
}

func jsString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	return `"` + s + `"`
}

// ---------------------------------------------------------------------------
// FileNode for sidebar tree
// ---------------------------------------------------------------------------

type FileNode struct {
	Name     string     `json:"name"`
	Path     string     `json:"path"`
	Type     string     `json:"type"` // "file" or "dir"
	Children []FileNode `json:"children,omitempty"`
}

func sidebarHTML(dirName, currentPath string, fileTree []FileNode) string {
	treeJSON, _ := json.Marshal(fileTree)
	return fmt.Sprintf(
		"<script>window.__servePath = %s; window.__serveFileTree = %s;</script>\n"+
			`<nav id="serve-sidebar">`+
			`<div id="serve-sidebar-header"><span class="dir-name">%s</span></div>`+
			`<div id="serve-sidebar-tree"></div>`+
			`</nav>`+
			`<button id="serve-sidebar-toggle">&lsaquo;</button>`+"\n",
		jsString(currentPath), string(treeJSON), htmlEscape(dirName),
	)
}

// ---------------------------------------------------------------------------
// Wrap functions — produce full HTML pages
// ---------------------------------------------------------------------------

type wrapOptions struct {
	sidebar   *[2]string // [dirName, currentPath]
	fileTree  []FileNode
	faviconPath string
	isMarp    bool
	pygmentsCSS string
}

func buildHead(title string, opts wrapOptions) string {
	var extra strings.Builder
	extra.WriteString(commentCSS)
	extra.WriteString(vimCSS)
	extra.WriteString(zoomCSS)
	if opts.isMarp {
		extra.WriteString(presentCSS)
	}
	if opts.sidebar != nil {
		extra.WriteString(sidebarCSS)
	}
	fav := faviconLink(opts.faviconPath)
	return fmt.Sprintf(headTemplate, htmlEscape(title), fav, opts.pygmentsCSS+extra.String())
}

func buildScripts(opts wrapOptions) string {
	var b strings.Builder
	b.WriteString("<script>" + reloadScript + "</script>\n")
	b.WriteString("<script>" + commentJS + "</script>\n")
	b.WriteString("<script>" + vimJS + "</script>\n")
	b.WriteString("<script>" + zoomJS + "</script>\n")
	if opts.isMarp {
		b.WriteString("<script>" + presentJS + "</script>\n")
	}
	if opts.sidebar != nil {
		b.WriteString("<script>" + sidebarJS + "</script>\n")
	}
	return b.String()
}

func wrapMarkdown(title, content, pygmentsCSS string, opts wrapOptions) string {
	opts.pygmentsCSS = pygmentsCSS
	var b strings.Builder
	b.WriteString(buildHead(title, opts))
	b.WriteString(headClose)
	if opts.sidebar != nil {
		b.WriteString(sidebarHTML(opts.sidebar[0], opts.sidebar[1], opts.fileTree))
	}
	b.WriteString(`<div id="serve-content">`)
	b.WriteString(content)
	b.WriteString("</div>\n")
	b.WriteString(commentHTML + "\n")
	b.WriteString(buildScripts(opts))
	b.WriteString(bodyClose)
	return b.String()
}

func wrapCode(title, highlightedHTML, pygmentsCSS string, opts wrapOptions) string {
	opts.pygmentsCSS = pygmentsCSS
	var b strings.Builder
	b.WriteString(buildHead(title, opts))
	b.WriteString(headClose)
	if opts.sidebar != nil {
		b.WriteString(sidebarHTML(opts.sidebar[0], opts.sidebar[1], opts.fileTree))
	}
	b.WriteString(`<div id="serve-content">`)
	b.WriteString(highlightedHTML)
	b.WriteString("</div>\n")
	b.WriteString("<script>" + reloadScript + "</script>\n")
	if opts.sidebar != nil {
		b.WriteString("<script>" + sidebarJS + "</script>\n")
	}
	b.WriteString(bodyClose)
	return b.String()
}

func wrapPlain(title, text string, opts wrapOptions) string {
	var b strings.Builder
	b.WriteString(buildHead(title, opts))
	b.WriteString(headClose)
	if opts.sidebar != nil {
		b.WriteString(sidebarHTML(opts.sidebar[0], opts.sidebar[1], opts.fileTree))
	}
	b.WriteString(`<div id="serve-content">`)
	b.WriteString(`<pre style="white-space:pre-wrap;word-break:break-word;">`)
	b.WriteString(htmlEscape(text))
	b.WriteString("</pre></div>\n")
	b.WriteString("<script>" + reloadScript + "</script>\n")
	if opts.sidebar != nil {
		b.WriteString("<script>" + sidebarJS + "</script>\n")
	}
	b.WriteString(bodyClose)
	return b.String()
}

func wrapPDF(title, pdfURL string, opts wrapOptions) string {
	var b strings.Builder
	b.WriteString(buildHead(title, opts))
	b.WriteString(`
    body { margin: 0; padding: 0; }
    body.has-sidebar { margin-left: 260px; }
    body.sidebar-collapsed { margin-left: 0; }
    embed { width: 100%; height: 100vh; border: none; }
`)
	b.WriteString(headClose)
	if opts.sidebar != nil {
		b.WriteString(sidebarHTML(opts.sidebar[0], opts.sidebar[1], opts.fileTree))
	}
	b.WriteString(fmt.Sprintf(`<embed src="/%s?raw=1" type="application/pdf">`, htmlEscape(pdfURL)))
	b.WriteString("\n<script>" + reloadScript + "</script>\n")
	if opts.sidebar != nil {
		b.WriteString("<script>" + sidebarJS + "</script>\n")
	}
	b.WriteString(bodyClose)
	return b.String()
}

func wrapImage(title, imageURL string, opts wrapOptions) string {
	var b strings.Builder
	b.WriteString(buildHead(title, opts))
	b.WriteString(`
    img.serve-image { max-width: 100%; height: auto; display: block; margin: 1em auto; border-radius: 4px; box-shadow: 0 2px 8px rgba(0,0,0,0.1); }
`)
	b.WriteString(headClose)
	if opts.sidebar != nil {
		b.WriteString(sidebarHTML(opts.sidebar[0], opts.sidebar[1], opts.fileTree))
	}
	b.WriteString(fmt.Sprintf(`<img class="serve-image" src="/%s?raw=1" alt="%s">`, htmlEscape(imageURL), htmlEscape(title)))
	b.WriteString("\n<script>" + reloadScript + "</script>\n")
	if opts.sidebar != nil {
		b.WriteString("<script>" + sidebarJS + "</script>\n")
	}
	b.WriteString(bodyClose)
	return b.String()
}

func formatSize(size int64) string {
	units := []string{"B", "KB", "MB", "GB"}
	f := float64(size)
	for _, u := range units {
		if f < 1024 {
			if u == "B" {
				return fmt.Sprintf("%.0f %s", f, u)
			}
			return fmt.Sprintf("%.1f %s", f, u)
		}
		f /= 1024
	}
	return fmt.Sprintf("%.1f TB", f)
}

func wrapFileInfo(title, fileURL string, size int64, opts wrapOptions) string {
	ext := "FILE"
	if idx := strings.LastIndex(title, "."); idx >= 0 {
		ext = strings.ToUpper(title[idx+1:])
	}
	var b strings.Builder
	b.WriteString(buildHead(title, opts))
	b.WriteString(`
    .file-info { text-align: center; padding: 4em 2em; }
    .file-info .icon { font-size: 64px; margin-bottom: 0.5em; }
    .file-info h2 { border-bottom: none; margin-bottom: 0.25em; }
    .file-info .meta { color: #656d76; font-size: 14px; margin-bottom: 1.5em; }
    .file-info .actions { display: flex; gap: 12px; justify-content: center; }
    .file-info .actions a { display: inline-block; padding: 8px 20px; border-radius: 6px; font-size: 14px; font-weight: 500; text-decoration: none; }
    .file-info .btn-download { background: #0078d4; color: #fff; }
    .file-info .btn-download:hover { background: #106ebe; text-decoration: none; }
    .file-info .btn-open { background: #f0f0f0; color: #24292e; }
    .file-info .btn-open:hover { background: #e0e0e0; text-decoration: none; }
`)
	b.WriteString(headClose)
	if opts.sidebar != nil {
		b.WriteString(sidebarHTML(opts.sidebar[0], opts.sidebar[1], opts.fileTree))
	}
	rawURL := "/" + htmlEscape(fileURL) + "?raw=1"
	b.WriteString(fmt.Sprintf(
		`<div class="file-info"><div class="icon">&#128196;</div><h2>%s</h2>`+
			`<div class="meta">%s &middot; %s</div>`+
			`<div class="actions">`+
			`<a class="btn-download" href="%s" download="%s">Download</a>`+
			`<a class="btn-open" href="%s" target="_blank">Open in new tab</a>`+
			`</div></div>`,
		htmlEscape(title), htmlEscape(ext), formatSize(size),
		rawURL, htmlEscape(title), rawURL,
	))
	b.WriteString("\n<script>" + reloadScript + "</script>\n")
	if opts.sidebar != nil {
		b.WriteString("<script>" + sidebarJS + "</script>\n")
	}
	b.WriteString(bodyClose)
	return b.String()
}

// ---------------------------------------------------------------------------
// _annotate_html_source_lines equivalent — used for raw HTML files
// ---------------------------------------------------------------------------

// annotateHTMLSourceLines adds data-source-lines to block-level elements in raw HTML.
func annotateHTMLSourceLines(html string) string {
	blockTags := map[string]bool{
		"p": true, "h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
		"div": true, "section": true, "article": true, "header": true, "footer": true,
		"nav": true, "aside": true, "main": true,
		"table": true, "thead": true, "tbody": true, "tfoot": true, "tr": true, "td": true, "th": true,
		"ul": true, "ol": true, "li": true, "dl": true, "dt": true, "dd": true,
		"blockquote": true, "pre": true, "figure": true, "figcaption": true,
		"details": true, "summary": true, "form": true, "fieldset": true,
	}

	// Build newline offset table
	newlineOffsets := []int{-1}
	for i, ch := range html {
		if ch == '\n' {
			newlineOffsets = append(newlineOffsets, i)
		}
	}
	offsetToLine := func(offset int) int {
		lo, hi := 0, len(newlineOffsets)-1
		for lo <= hi {
			mid := (lo + hi) / 2
			if newlineOffsets[mid] < offset {
				lo = mid + 1
			} else {
				hi = mid - 1
			}
		}
		return lo
	}

	var result strings.Builder
	i := 0
	skipDepth := 0
	n := len(html)

	for i < n {
		if html[i] != '<' {
			result.WriteByte(html[i])
			i++
			continue
		}
		// Find end of tag
		end := strings.Index(html[i:], ">")
		if end < 0 {
			result.WriteByte(html[i])
			i++
			continue
		}
		end += i + 1
		tagContent := html[i+1 : end-1]
		isClose := strings.HasPrefix(tagContent, "/")
		if isClose {
			tagContent = tagContent[1:]
		}
		// Extract tag name
		tagName := ""
		for j, ch := range tagContent {
			if ch == ' ' || ch == '\t' || ch == '\n' || ch == '/' {
				tagName = tagContent[:j]
				break
			}
			tagName = tagContent[:j+1]
		}
		tagNameLower := strings.ToLower(tagName)

		if tagNameLower == "script" || tagNameLower == "style" {
			if isClose {
				if skipDepth > 0 {
					skipDepth--
				}
			} else {
				skipDepth++
			}
			result.WriteString(html[i:end])
			i = end
			continue
		}

		if skipDepth > 0 || isClose || !blockTags[tagNameLower] {
			result.WriteString(html[i:end])
			i = end
			continue
		}

		// Check if already has data-source-lines
		if strings.Contains(tagContent, "data-source-lines") {
			result.WriteString(html[i:end])
			i = end
			continue
		}

		lineNum := offsetToLine(i)
		// Insert attribute before closing >
		result.WriteString(html[i : end-1])
		result.WriteString(fmt.Sprintf(` data-source-lines="%d-%d">`, lineNum, lineNum))
		i = end
	}
	return result.String()
}

// injectReloadScript injects scripts into an existing HTML document.
func injectReloadScript(html string, sidebar *[2]string, fileTree []FileNode, faviconPath string, annotate, bare bool) string {
	if annotate {
		html = annotateHTMLSourceLines(html)
	}

	favTag := faviconLink(faviconPath)

	var cssTag, scripts string
	if bare {
		scripts = "<script>" + reloadScript + "</script>"
	} else {
		var cssParts strings.Builder
		cssParts.WriteString(commentCSS)
		cssParts.WriteString(vimCSS)
		if sidebar != nil {
			cssParts.WriteString(sidebarCSS)
		}
		cssTag = "<style>" + cssParts.String() + "</style>"

		var scriptParts strings.Builder
		if sidebar != nil {
			scriptParts.WriteString(sidebarHTML(sidebar[0], sidebar[1], fileTree))
		}
		scriptParts.WriteString(commentHTML + "\n")
		scriptParts.WriteString("<script>" + reloadScript + "</script>\n")
		scriptParts.WriteString("<script>" + commentJS + "</script>\n")
		scriptParts.WriteString("<script>" + vimJS + "</script>\n")
		if sidebar != nil {
			scriptParts.WriteString("<script>" + sidebarJS + "</script>\n")
		}
		scripts = scriptParts.String()
	}

	if strings.Contains(html, "</head>") {
		html = strings.Replace(html, "</head>", favTag+"</head>", 1)
	}

	inject := cssTag + "\n" + scripts + "\n"
	if strings.Contains(html, "</body>") {
		html = strings.Replace(html, "</body>", inject+"</body>", 1)
	} else if strings.Contains(html, "</html>") {
		html = strings.Replace(html, "</html>", inject+"</html>", 1)
	} else {
		html = html + "\n" + inject
	}
	return html
}
