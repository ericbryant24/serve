"""HTML templates and reload script for the serve tool."""

import base64
import hashlib

# ---------------------------------------------------------------------------
# Favicon generation — deterministic per-path emoji + color
# ---------------------------------------------------------------------------

_FAVICON_EMOJIS = [
    "\U0001F4D8", "\U0001F4D5", "\U0001F4D7", "\U0001F4D9", "\U0001F4D3",  # books
    "\U0001F4DD", "\U0001F4CB", "\U0001F4C4", "\U0001F4C3", "\U0001F4DC",  # docs
    "\U0001F5DE", "\U0001F4F0", "\U0001F4DA", "\U0001F4D6", "\U0001F4D4",  # reading
    "\U0001F52C", "\U0001F9EA", "\U0001F9EC", "\U0001F52D", "\U0001F4A1",  # science
    "\U0001F3A8", "\U0001F308", "\U0001F525", "\U0001F4A7", "\U0001F331",  # nature/art
    "\U0001F680", "\U0001F6F8", "\U0001F30D", "\U0001F30A", "\U0001F30B",  # space/earth
    "\U0001F3B2", "\U0001F3AF", "\U0001F3B0", "\U0001F3B3", "\U0001F3AE",  # games
    "\U0001F981", "\U0001F985", "\U0001F989", "\U0001F419", "\U0001F98B",  # animals
]

_FAVICON_COLORS = [
    "#264653", "#2a9d8f", "#e9c46a", "#f4a261", "#e76f51",
    "#606c38", "#283618", "#dda15e", "#bc6c25", "#6d6875",
    "#b5838d", "#e5989b", "#ffb4a2", "#457b9d", "#1d3557",
    "#a8dadc", "#2b2d42", "#8d99ae", "#ef233c", "#d90429",
]


def _favicon_for_path(path: str) -> tuple[str, str]:
    """Return (emoji, bg_color) deterministically chosen from *path*."""
    h = int(hashlib.md5(path.encode()).hexdigest(), 16)
    emoji = _FAVICON_EMOJIS[h % len(_FAVICON_EMOJIS)]
    color = _FAVICON_COLORS[(h >> 8) % len(_FAVICON_COLORS)]
    return emoji, color


def _favicon_link(path: str) -> str:
    """Return an HTML <link> tag with an inline SVG favicon for *path*."""
    emoji, bg = _favicon_for_path(path)
    svg = (
        f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100">'
        f'<rect width="100" height="100" rx="20" fill="{bg}"/>'
        f'<text x="50" y="72" font-size="60" text-anchor="middle">{emoji}</text>'
        f'</svg>'
    )
    # Inline as a data URI — no caching issues
    b64 = base64.b64encode(svg.encode()).decode()
    return f'  <link rel="icon" href="data:image/svg+xml;base64,{b64}">\n'


RELOAD_SCRIPT = """\
(function() {
  function connect() {
    var ws = new WebSocket('ws://' + location.host + '/ws');
    ws.onmessage = function(event) {
      var data = JSON.parse(event.data);
      if (data.type === 'reload') {
        location.reload();
      }
    };
    ws.onclose = function() {
      setTimeout(connect, 1000);
    };
  }
  connect();
})();"""

# ---------------------------------------------------------------------------
# Comment CSS
# ---------------------------------------------------------------------------

_COMMENT_CSS = """\
    /* Comment highlights */
    mark.comment-highlight {
      background: rgba(255, 213, 79, 0.35);
      cursor: pointer;
      border-radius: 2px;
      transition: background 0.15s;
    }
    mark.comment-highlight:hover {
      background: rgba(255, 213, 79, 0.55);
    }
    mark.comment-highlight.resolved {
      background: rgba(76, 175, 80, 0.18);
    }
    mark.comment-highlight.resolved:hover {
      background: rgba(76, 175, 80, 0.35);
    }

    /* Floating "Comment" button */
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

    /* Comment popover */
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

    /* Comment thread */
    .comment-thread {
      border-left: 3px solid #0078d4;
      margin: 8px;
      border-radius: 4px;
      background: #f8f9fa;
    }
    .comment-thread.resolved {
      border-left-color: #22c55e;
      opacity: 0.75;
    }
    .comment-thread.resolved:hover { opacity: 1; }
    .comment-card {
      padding: 10px 14px;
    }
    .comment-card.reply {
      padding-left: 28px;
      border-top: 1px solid #e8e8e8;
      background: #fdfdfe;
    }
    .comment-meta {
      font-size: 11px;
      color: #656d76;
      margin-bottom: 4px;
    }
    .comment-text {
      font-size: 13px;
      line-height: 1.5;
      white-space: pre-wrap;
      word-break: break-word;
    }
    .comment-actions {
      margin-top: 6px;
      display: flex;
      gap: 6px;
    }
    .comment-actions button {
      background: none;
      border: 1px solid transparent;
      border-radius: 4px;
      padding: 2px 8px;
      font-size: 12px;
      cursor: pointer;
    }
    .comment-actions .btn-reply { color: #0078d4; }
    .comment-actions .btn-reply:hover { background: #e8f0fe; }
    .comment-actions .btn-resolve { color: #22c55e; }
    .comment-actions .btn-resolve:hover { background: #f0fdf0; }
    .comment-actions .btn-unresolve { color: #656d76; }
    .comment-actions .btn-unresolve:hover { background: #f0f0f0; }
    .comment-actions .btn-delete { color: #d73a49; }
    .comment-actions .btn-delete:hover { background: #fff0f0; }

    .resolved-badge {
      display: inline-block;
      background: #f0fdf0;
      color: #22c55e;
      font-size: 12px;
      font-weight: 600;
      padding: 2px 8px;
      border-radius: 4px;
    }

    /* Comment form */
    .comment-form {
      padding: 10px 14px;
    }
    .comment-form textarea {
      width: 100%;
      min-height: 60px;
      border: 1px solid #d0d7de;
      border-radius: 6px;
      padding: 8px;
      font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Helvetica, Arial, sans-serif;
      font-size: 13px;
      resize: vertical;
      box-sizing: border-box;
    }
    .comment-form textarea:focus {
      outline: none;
      border-color: #0078d4;
      box-shadow: 0 0 0 2px rgba(0, 120, 212, 0.2);
    }
    .comment-form .hint {
      font-size: 11px;
      color: #656d76;
      margin-top: 4px;
    }
    .comment-form-actions {
      display: flex;
      gap: 8px;
      margin-top: 8px;
      justify-content: flex-end;
    }
    .comment-form-actions button {
      border: none;
      border-radius: 6px;
      padding: 5px 14px;
      font-size: 13px;
      cursor: pointer;
    }
    .comment-form-actions .btn-cancel {
      background: #f0f0f0;
      color: #24292e;
    }
    .comment-form-actions .btn-cancel:hover { background: #e0e0e0; }
    .comment-form-actions .btn-submit {
      background: #0078d4;
      color: #fff;
    }
    .comment-form-actions .btn-submit:hover { background: #106ebe; }

    /* Orphaned comments section */
    .orphaned-comments {
      margin-top: 2em;
      padding: 1em;
      border: 1px dashed #d0d7de;
      border-radius: 8px;
      background: #fffbeb;
    }
    .orphaned-comments h3 {
      margin-top: 0;
      font-size: 14px;
      color: #9a6700;
    }

    /* Comment count badge */
    .comment-count-badge {
      position: fixed;
      bottom: 20px;
      right: 20px;
      background: #0078d4;
      color: #fff;
      border-radius: 20px;
      padding: 8px 16px;
      font-size: 13px;
      font-weight: 500;
      cursor: pointer;
      box-shadow: 0 2px 8px rgba(0,0,0,0.18);
      z-index: 1001;
      user-select: none;
    }
    .comment-count-badge:hover { background: #106ebe; }
    .comment-count-badge.has-unresolved { background: #e36209; }

    /* Comment panel (slide-in sidebar) */
    .comment-panel {
      position: fixed;
      top: 0;
      right: 0;
      width: 380px;
      height: 100vh;
      background: #fff;
      border-left: 1px solid #d0d7de;
      box-shadow: -4px 0 16px rgba(0,0,0,0.08);
      z-index: 1000;
      overflow-y: auto;
      transform: translateX(100%);
      transition: transform 0.25s ease;
    }
    .comment-panel.open {
      transform: translateX(0);
    }
    .comment-panel-header {
      position: sticky;
      top: 0;
      background: #fff;
      padding: 16px 20px;
      border-bottom: 1px solid #e8e8e8;
      display: flex;
      align-items: center;
      justify-content: space-between;
      z-index: 1;
    }
    .comment-panel-header h3 {
      margin: 0;
      font-size: 15px;
      font-weight: 600;
    }
    .comment-panel-close {
      background: none;
      border: none;
      font-size: 20px;
      cursor: pointer;
      color: #656d76;
      padding: 0 4px;
      line-height: 1;
    }
    .comment-panel-close:hover { color: #24292e; }
    .comment-panel-body {
      padding: 12px;
    }
    .panel-comment-item {
      border: 1px solid #e8e8e8;
      border-radius: 8px;
      margin-bottom: 10px;
      overflow: hidden;
      cursor: pointer;
      transition: border-color 0.15s;
    }
    .panel-comment-item:hover {
      border-color: #0078d4;
    }
    .panel-comment-item.resolved {
      opacity: 0.65;
    }
    .panel-comment-item.resolved:hover {
      opacity: 1;
    }
    .panel-comment-anchor {
      background: #fffbeb;
      padding: 8px 12px;
      font-size: 12px;
      color: #9a6700;
      border-bottom: 1px solid #e8e8e8;
      white-space: nowrap;
      overflow: hidden;
      text-overflow: ellipsis;
    }
    .panel-comment-item.resolved .panel-comment-anchor {
      background: #f0fdf0;
      color: #22c55e;
    }
    .panel-comment-body {
      padding: 10px 12px;
    }
    .panel-comment-body .comment-text {
      font-size: 13px;
      margin-bottom: 4px;
    }
    .panel-comment-body .comment-meta {
      font-size: 11px;
      color: #656d76;
    }
    .panel-comment-replies {
      font-size: 11px;
      color: #656d76;
      padding: 0 12px 8px;
    }
"""

# ---------------------------------------------------------------------------
# Comment JavaScript
# ---------------------------------------------------------------------------

_COMMENT_JS = """\
(function() {
  var comments = [];
  var pendingSelection = null;
  var activePopover = null;

  // --- API helpers ---
  var _fileParam = window.__servePath ? '?file=' + encodeURIComponent(window.__servePath) : '';
  function api(method, path, body) {
    var opts = { method: method, headers: { 'Content-Type': 'application/json' } };
    if (body) opts.body = JSON.stringify(body);
    var sep = (path || '').indexOf('?') >= 0 ? '&' : '?';
    var suffix = _fileParam ? (path ? sep + _fileParam.substring(1) : _fileParam) : '';
    return fetch('/api/comments' + (path || '') + suffix, opts).then(function(r) { return r.json(); });
  }

  // --- Escaping ---
  function esc(s) {
    var d = document.createElement('div');
    d.textContent = s;
    return d.innerHTML;
  }

  // --- Time formatting ---
  function timeAgo(iso) {
    var diff = (Date.now() - new Date(iso).getTime()) / 1000;
    if (diff < 60) return 'just now';
    if (diff < 3600) return Math.floor(diff / 60) + 'm ago';
    if (diff < 86400) return Math.floor(diff / 3600) + 'h ago';
    return Math.floor(diff / 86400) + 'd ago';
  }

  // --- Init ---
  function init() {
    api('GET', '').then(function(res) {
      comments = res.comments || [];
      applyHighlights();
      updateBadge();
    });
    setupSelectionListener();
  }

  // --- Text selection → Comment button ---
  function setupSelectionListener() {
    var btn = document.getElementById('comment-btn');

    document.addEventListener('mouseup', function(e) {
      // Ignore clicks inside popovers or forms
      if (e.target.closest && e.target.closest('.comment-popover, .comment-form, #comment-btn')) return;

      setTimeout(function() {
        var sel = window.getSelection();
        if (!sel || sel.isCollapsed || !sel.toString().trim()) {
          btn.style.display = 'none';
          pendingSelection = null;
          return;
        }

        var range = sel.getRangeAt(0);
        var rect = range.getBoundingClientRect();
        var anchorText = sel.toString().trim();

        // Find the nearest block element with data-source-lines
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

        pendingSelection = {
          anchorText: anchorText,
          blockText: blockText,
          sourceLines: sourceLines,
          block: block
        };

        btn.style.display = 'block';
        btn.style.left = (window.scrollX + rect.left + rect.width / 2 - 30) + 'px';
        btn.style.top = (window.scrollY + rect.bottom + 6) + 'px';
      }, 10);
    });

    document.addEventListener('mousedown', function(e) {
      if (e.target.closest && e.target.closest('#comment-btn, .comment-popover, .comment-form')) return;
      btn.style.display = 'none';
      closePopover();
    });

    btn.addEventListener('click', function(e) {
      e.preventDefault();
      e.stopPropagation();
      btn.style.display = 'none';
      if (pendingSelection) openCommentForm(pendingSelection);
    });
  }

  // --- Comment form ---
  function openCommentForm(selInfo, parentId) {
    closePopover();
    var popover = document.createElement('div');
    popover.className = 'comment-popover';

    var block = selInfo.block;
    if (block) {
      var rect = block.getBoundingClientRect();
      popover.style.left = (window.scrollX + rect.left) + 'px';
      popover.style.top = (window.scrollY + rect.bottom + 8) + 'px';
    } else {
      popover.style.left = '50px';
      popover.style.top = (window.scrollY + 100) + 'px';
    }

    popover.innerHTML =
      '<div class="comment-form">' +
        '<textarea placeholder="Write a comment..." autofocus></textarea>' +
        '<div class="hint">Ctrl+Enter to submit · Escape to cancel</div>' +
        '<div class="comment-form-actions">' +
          '<button class="btn-cancel">Cancel</button>' +
          '<button class="btn-submit">Comment</button>' +
        '</div>' +
      '</div>';

    document.body.appendChild(popover);
    activePopover = popover;

    var ta = popover.querySelector('textarea');
    ta.focus();

    ta.addEventListener('keydown', function(e) {
      if (e.key === 'Escape') { closePopover(); }
      if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
        submitComment(selInfo, ta.value.trim(), parentId);
      }
    });

    popover.querySelector('.btn-cancel').addEventListener('click', closePopover);
    popover.querySelector('.btn-submit').addEventListener('click', function() {
      submitComment(selInfo, ta.value.trim(), parentId);
    });
  }

  function submitComment(selInfo, text, parentId) {
    if (!text) return;
    var body = {
      text: text,
      anchor_text: selInfo.anchorText || '',
      block_text: selInfo.blockText || ''
    };
    if (selInfo.sourceLines) {
      body.source_line_start = selInfo.sourceLines.start;
      body.source_line_end = selInfo.sourceLines.end;
    }
    if (parentId) body.parent_id = parentId;

    api('POST', '', body).then(function(comment) {
      comments.push(comment);
      closePopover();
      clearHighlights();
      applyHighlights();
      updateBadge();
      window.getSelection().removeAllRanges();
    });
  }

  // --- Popover management ---
  function closePopover() {
    if (activePopover) {
      activePopover.remove();
      activePopover = null;
    }
  }

  // --- Highlights ---
  function clearHighlights() {
    document.querySelectorAll('mark.comment-highlight').forEach(function(mark) {
      var parent = mark.parentNode;
      while (mark.firstChild) parent.insertBefore(mark.firstChild, mark);
      parent.removeChild(mark);
      parent.normalize();
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
    // Try to find the container by source lines first
    var containers = [];
    if (lineStart) {
      document.querySelectorAll('[data-source-lines]').forEach(function(el) {
        var parts = el.getAttribute('data-source-lines').split('-');
        var s = parseInt(parts[0], 10), e = parseInt(parts[1], 10);
        if (s <= lineStart && e >= lineEnd) containers.push(el);
      });
      // Use the most specific (innermost) match
      if (containers.length > 1) {
        containers.sort(function(a, b) {
          return a.textContent.length - b.textContent.length;
        });
      }
    }

    var searchRoot = containers.length > 0 ? containers[0] : document.querySelector('body');

    // Walk text nodes to find the anchor text
    var walker = document.createTreeWalker(searchRoot, NodeFilter.SHOW_TEXT);
    var textNodes = [];
    var fullText = '';
    while (walker.nextNode()) {
      var n = walker.currentNode;
      if (n.parentElement.closest('mark.comment-highlight, .comment-popover, .comment-form, .orphaned-comments, script, style')) continue;
      textNodes.push({ node: n, start: fullText.length });
      fullText += n.textContent;
    }

    var idx = fullText.indexOf(anchorText);
    if (idx === -1) return false;

    // Find the text nodes that span this range
    var remaining = anchorText.length;
    var pos = idx;
    for (var i = 0; i < textNodes.length && remaining > 0; i++) {
      var tn = textNodes[i];
      var nodeEnd = tn.start + tn.node.textContent.length;
      if (nodeEnd <= pos) continue;

      var offsetInNode = Math.max(0, pos - tn.start);
      var charsInNode = Math.min(tn.node.textContent.length - offsetInNode, remaining);

      var range = document.createRange();
      range.setStart(tn.node, offsetInNode);
      range.setEnd(tn.node, offsetInNode + charsInNode);

      var mark = document.createElement('mark');
      mark.className = 'comment-highlight' + (resolved ? ' resolved' : '');
      mark.setAttribute('data-comment-id', commentId);
      mark.addEventListener('click', (function(cid) {
        return function(e) {
          e.stopPropagation();
          showCommentThread(cid, e.target);
        };
      })(commentId));

      try {
        range.surroundContents(mark);
      } catch (ex) {
        // Cross-element selection — wrap what we can
        var fragment = range.extractContents();
        mark.appendChild(fragment);
        range.insertNode(mark);
      }

      remaining -= charsInNode;
      pos += charsInNode;
      // After DOM mutation, re-collect text nodes for remaining portions
      if (remaining > 0) {
        walker = document.createTreeWalker(searchRoot, NodeFilter.SHOW_TEXT);
        textNodes = [];
        fullText = '';
        while (walker.nextNode()) {
          var nn = walker.currentNode;
          if (nn.parentElement.closest('mark.comment-highlight, .comment-popover, .comment-form, .orphaned-comments, script, style')) continue;
          textNodes.push({ node: nn, start: fullText.length });
          fullText += nn.textContent;
        }
        idx = fullText.indexOf(anchorText.substring(anchorText.length - remaining));
        if (idx === -1) break;
        pos = idx;
      }
    }
    return true;
  }

  // --- Show comment thread ---
  function showCommentThread(commentId, targetEl) {
    closePopover();
    var root = comments.find(function(c) { return c.id === commentId; });
    if (!root) return;

    var replies = comments.filter(function(c) { return c.parent_id === commentId; });
    replies.sort(function(a, b) { return a.created_at.localeCompare(b.created_at); });

    var popover = document.createElement('div');
    popover.className = 'comment-popover';

    var rect = targetEl.getBoundingClientRect();
    popover.style.left = (window.scrollX + rect.left) + 'px';
    popover.style.top = (window.scrollY + rect.bottom + 8) + 'px';

    popover.innerHTML = renderThread(root, replies);
    document.body.appendChild(popover);
    activePopover = popover;

    // Wire up buttons
    popover.querySelectorAll('[data-action]').forEach(function(btn) {
      btn.addEventListener('click', function() {
        var action = btn.getAttribute('data-action');
        var id = btn.getAttribute('data-id');
        if (action === 'resolve') toggleResolve(id, true);
        else if (action === 'unresolve') toggleResolve(id, false);
        else if (action === 'delete') deleteComment(id);
        else if (action === 'reply') {
          var selInfo = {
            anchorText: root.anchor_text,
            blockText: root.block_text,
            sourceLines: root.source_line_start ? { start: root.source_line_start, end: root.source_line_end } : null,
            block: targetEl.closest('[data-source-lines]') || targetEl.closest('p, h1, h2, h3, h4, h5, h6, li, blockquote, td, th, pre, div')
          };
          closePopover();
          openCommentForm(selInfo, commentId);
        }
      });
    });
  }

  function renderThread(root, replies) {
    var cls = 'comment-thread' + (root.resolved ? ' resolved' : '');
    var html = '<div class="' + cls + '">';

    if (root.resolved) {
      html += '<div class="comment-card" style="display:flex;align-items:center;justify-content:space-between;">' +
        '<span class="resolved-badge">&#10003; Resolved</span>' +
        '<div>' +
          '<button data-action="unresolve" data-id="' + root.id + '" class="comment-actions btn-unresolve" style="border:none;background:none;cursor:pointer;font-size:12px;">Unresolve</button>' +
          '<button data-action="delete" data-id="' + root.id + '" class="comment-actions btn-delete" style="border:none;background:none;cursor:pointer;font-size:12px;">Delete</button>' +
        '</div></div>';
    }

    html += '<div class="comment-card">' +
      '<div class="comment-meta">' + timeAgo(root.created_at) + '</div>' +
      '<div class="comment-text">' + esc(root.text) + '</div>' +
      '<div class="comment-actions">' +
        '<button data-action="reply" data-id="' + root.id + '" class="btn-reply">Reply</button>';
    if (!root.resolved) {
      html += '<button data-action="resolve" data-id="' + root.id + '" class="btn-resolve">Resolve</button>';
    }
    html += '<button data-action="delete" data-id="' + root.id + '" class="btn-delete">Delete</button>' +
      '</div></div>';

    replies.forEach(function(r) {
      html += '<div class="comment-card reply">' +
        '<div class="comment-meta">' + timeAgo(r.created_at) + '</div>' +
        '<div class="comment-text">' + esc(r.text) + '</div>' +
        '<div class="comment-actions">' +
          '<button data-action="delete" data-id="' + r.id + '" class="btn-delete">Delete</button>' +
        '</div></div>';
    });

    html += '</div>';
    return html;
  }

  // --- Resolve / Delete ---
  function toggleResolve(commentId, resolved) {
    api('PATCH', '/' + commentId, { resolved: resolved }).then(function(updated) {
      var idx = comments.findIndex(function(c) { return c.id === commentId; });
      if (idx >= 0) comments[idx] = updated;
      closePopover();
      clearHighlights();
      applyHighlights();
      updateBadge();
    });
  }

  function deleteComment(commentId) {
    api('DELETE', '/' + commentId).then(function() {
      comments = comments.filter(function(c) { return c.id !== commentId && c.parent_id !== commentId; });
      closePopover();
      clearHighlights();
      applyHighlights();
      updateBadge();
    });
  }

  // --- Orphaned comments ---
  function renderOrphaned(orphaned) {
    var existing = document.querySelector('.orphaned-comments');
    if (existing) existing.remove();
    if (orphaned.length === 0) return;

    var section = document.createElement('div');
    section.className = 'orphaned-comments';
    section.innerHTML = '<h3>Unanchored Comments</h3>';

    orphaned.forEach(function(c) {
      var replies = comments.filter(function(r) { return r.parent_id === c.id; });
      section.innerHTML += renderThread(c, replies);
    });

    document.body.appendChild(section);

    // Wire up buttons in orphaned section
    section.querySelectorAll('[data-action]').forEach(function(btn) {
      btn.addEventListener('click', function() {
        var action = btn.getAttribute('data-action');
        var id = btn.getAttribute('data-id');
        if (action === 'resolve') toggleResolve(id, true);
        else if (action === 'unresolve') toggleResolve(id, false);
        else if (action === 'delete') deleteComment(id);
      });
    });
  }

  // --- Badge & Panel ---
  function updateBadge() {
    var badge = document.getElementById('comment-badge');
    var roots = comments.filter(function(c) { return !c.parent_id; });
    var unresolved = roots.filter(function(c) { return !c.resolved; });
    if (roots.length === 0) {
      badge.style.display = 'none';
      return;
    }
    badge.style.display = 'block';
    badge.textContent = unresolved.length > 0
      ? unresolved.length + ' comment' + (unresolved.length !== 1 ? 's' : '')
      : roots.length + ' resolved';
    badge.className = 'comment-count-badge' + (unresolved.length > 0 ? ' has-unresolved' : '');
  }

  function setupPanel() {
    var badge = document.getElementById('comment-badge');
    var panel = document.getElementById('comment-panel');
    var closeBtn = document.getElementById('panel-close');

    badge.addEventListener('click', function() {
      renderPanel();
      panel.classList.toggle('open');
    });
    closeBtn.addEventListener('click', function() {
      panel.classList.remove('open');
    });
  }

  function renderPanel() {
    var body = document.getElementById('panel-body');
    var roots = comments.filter(function(c) { return !c.parent_id; });
    roots.sort(function(a, b) { return a.created_at.localeCompare(b.created_at); });

    if (roots.length === 0) {
      body.innerHTML = '<p style="color:#656d76;font-size:13px;text-align:center;padding:2em 0;">No comments yet. Select text to add one.</p>';
      return;
    }

    var html = '';
    roots.forEach(function(c) {
      var replies = comments.filter(function(r) { return r.parent_id === c.id; });
      var anchor = c.anchor_text || '(no selection)';
      if (anchor.length > 60) anchor = anchor.substring(0, 60) + '...';
      var cls = 'panel-comment-item' + (c.resolved ? ' resolved' : '');
      var badge = c.resolved ? '<span class="resolved-badge" style="font-size:10px;padding:1px 6px;margin-left:6px;">resolved</span>' : '';

      html += '<div class="' + cls + '" data-panel-comment="' + c.id + '">';
      html += '<div class="panel-comment-anchor">"' + esc(anchor) + '"' + badge + '</div>';
      html += '<div class="panel-comment-body">';
      html += '<div class="comment-text">' + esc(c.text) + '</div>';
      html += '<div class="comment-meta">' + timeAgo(c.created_at);
      if (c.source_line_start) html += ' · line ' + c.source_line_start;
      html += '</div>';
      html += '</div>';
      if (replies.length > 0) {
        html += '<div class="panel-comment-replies">' + replies.length + ' repl' + (replies.length === 1 ? 'y' : 'ies') + '</div>';
      }
      html += '</div>';
    });
    body.innerHTML = html;

    // Click a panel item → scroll to highlight and open thread
    body.querySelectorAll('[data-panel-comment]').forEach(function(item) {
      item.addEventListener('click', function() {
        var cid = item.getAttribute('data-panel-comment');
        var mark = document.querySelector('mark[data-comment-id="' + cid + '"]');
        if (mark) {
          mark.scrollIntoView({ behavior: 'smooth', block: 'center' });
          setTimeout(function() { showCommentThread(cid, mark); }, 350);
        } else {
          // Orphaned — just show thread in a popover at the panel edge
          showCommentThread(cid, item);
        }
        document.getElementById('comment-panel').classList.remove('open');
      });
    });
  }

  // Run on load
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', function() { init(); setupPanel(); });
  } else {
    init(); setupPanel();
  }
})();"""

# ---------------------------------------------------------------------------
# Markdown template (split into parts to avoid double-brace escaping in JS)
# ---------------------------------------------------------------------------

_HEAD_TEMPLATE = """\
<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{title}</title>
{favicon}
  <style>
    body {{
      max-width: 48em;
      margin: 2em auto;
      padding: 0 1em;
      font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Helvetica, Arial, sans-serif;
      line-height: 1.6;
      color: #24292e;
      background: #fff;
    }}
    h1, h2, h3, h4, h5, h6 {{
      margin-top: 1.5em;
      margin-bottom: 0.5em;
      font-weight: 600;
      line-height: 1.25;
    }}
    h1 {{ font-size: 2em; border-bottom: 1px solid #eaecef; padding-bottom: 0.3em; }}
    h2 {{ font-size: 1.5em; border-bottom: 1px solid #eaecef; padding-bottom: 0.3em; }}
    a {{ color: #0366d6; text-decoration: none; }}
    a:hover {{ text-decoration: underline; }}
    pre {{
      background: #f6f8fa;
      padding: 1em;
      overflow-x: auto;
      border-radius: 6px;
      line-height: 1.45;
    }}
    code {{
      font-family: "SFMono-Regular", Consolas, "Liberation Mono", Menlo, monospace;
      font-size: 0.875em;
    }}
    :not(pre) > code {{
      background: #f6f8fa;
      padding: 0.2em 0.4em;
      border-radius: 3px;
    }}
    img {{ max-width: 100%; height: auto; }}
    table {{ border-collapse: collapse; width: 100%; margin: 1em 0; }}
    th, td {{ border: 1px solid #dfe2e5; padding: 0.5em 0.75em; text-align: left; }}
    th {{ background: #f6f8fa; font-weight: 600; }}
    tr:nth-child(2n) {{ background: #f6f8fa; }}
    blockquote {{
      border-left: 4px solid #dfe2e5;
      margin: 0;
      padding: 0 1em;
      color: #6a737d;
    }}
    hr {{ border: none; border-top: 1px solid #eaecef; margin: 1.5em 0; }}
    .highlight {{ background: #f6f8fa; border-radius: 6px; }}
    .highlight pre {{ background: transparent; margin: 0; }}
    {pygments_css}
"""

_HEAD_CLOSE = """\
  </style>
  <script type="module">
    import mermaid from 'https://cdn.jsdelivr.net/npm/mermaid@11/dist/mermaid.esm.min.mjs';
    mermaid.initialize({ startOnLoad: true, theme: 'default' });
  </script>
</head>
<body>
"""

_BODY_CLOSE = """\
</body>
</html>"""

# ---------------------------------------------------------------------------
# Sidebar CSS (directory mode)
# ---------------------------------------------------------------------------

_SIDEBAR_CSS = """\
    /* Sidebar */
    #serve-sidebar {
      position: fixed;
      top: 0;
      left: 0;
      width: 260px;
      height: 100vh;
      background: #f6f8fa;
      border-right: 1px solid #d0d7de;
      overflow-y: auto;
      z-index: 900;
      font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Helvetica, Arial, sans-serif;
      font-size: 13px;
      transform: translateX(0);
      transition: transform 0.2s ease;
    }
    #serve-sidebar.collapsed {
      transform: translateX(-260px);
    }
    #serve-sidebar-header {
      position: sticky;
      top: 0;
      background: #f6f8fa;
      padding: 14px 16px;
      border-bottom: 1px solid #d0d7de;
      font-weight: 600;
      font-size: 14px;
      color: #24292e;
      display: flex;
      align-items: center;
      justify-content: space-between;
      z-index: 1;
    }
    #serve-sidebar-header .dir-name {
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }
    #serve-sidebar-toggle {
      position: fixed;
      top: 10px;
      left: 268px;
      z-index: 901;
      background: #f6f8fa;
      border: 1px solid #d0d7de;
      border-radius: 6px;
      width: 28px;
      height: 28px;
      display: flex;
      align-items: center;
      justify-content: center;
      cursor: pointer;
      font-size: 16px;
      color: #656d76;
      transition: left 0.2s ease;
      line-height: 1;
    }
    #serve-sidebar-toggle:hover { background: #e8e8e8; }
    #serve-sidebar-toggle.collapsed { left: 8px; }
    #serve-sidebar-tree {
      padding: 8px 0;
    }
    .sidebar-dir, .sidebar-file {
      display: block;
      padding: 3px 12px 3px 0;
      color: #24292e;
      text-decoration: none;
      cursor: pointer;
      white-space: nowrap;
      overflow: hidden;
      text-overflow: ellipsis;
      border-radius: 4px;
      margin: 0 6px;
    }
    .sidebar-dir {
      font-weight: 500;
      user-select: none;
    }
    .sidebar-dir:hover, .sidebar-file:hover {
      background: #e8e8e8;
    }
    .sidebar-file.active {
      background: #ddf4ff;
      color: #0366d6;
      font-weight: 500;
    }
    .sidebar-dir::before {
      content: '\\25B6';
      display: inline-block;
      font-size: 9px;
      margin-right: 4px;
      transition: transform 0.15s;
    }
    .sidebar-dir.open::before {
      transform: rotate(90deg);
    }
    .sidebar-children {
      overflow: hidden;
    }
    .sidebar-children.collapsed {
      display: none;
    }
    .sidebar-icon {
      display: inline-block;
      width: 16px;
      text-align: center;
      margin-right: 4px;
      font-size: 12px;
    }

    /* Push body content when sidebar is open */
    body.has-sidebar {
      margin-left: 280px;
    }
    body.sidebar-collapsed {
      margin-left: 20px;
    }
"""

# ---------------------------------------------------------------------------
# Sidebar JavaScript (directory mode)
# ---------------------------------------------------------------------------

_SIDEBAR_JS = """\
(function() {
  var STORAGE_KEY = 'serve-sidebar';
  var sidebar = document.getElementById('serve-sidebar');
  var toggle = document.getElementById('serve-sidebar-toggle');
  var tree = document.getElementById('serve-sidebar-tree');
  var currentPath = window.__servePath || '';

  // --- Collapse state ---
  function getState() {
    try { return JSON.parse(localStorage.getItem(STORAGE_KEY) || '{}'); }
    catch(e) { return {}; }
  }
  function saveState(s) {
    try { localStorage.setItem(STORAGE_KEY, JSON.stringify(s)); } catch(e) {}
  }

  // --- Sidebar toggle ---
  var state = getState();
  if (state.hidden) {
    sidebar.classList.add('collapsed');
    toggle.classList.add('collapsed');
    document.body.classList.add('sidebar-collapsed');
    document.body.classList.remove('has-sidebar');
    toggle.textContent = '\\u2630';
  } else {
    document.body.classList.add('has-sidebar');
    toggle.textContent = '\\u2039';
  }

  toggle.addEventListener('click', function() {
    var s = getState();
    s.hidden = !s.hidden;
    saveState(s);
    sidebar.classList.toggle('collapsed');
    toggle.classList.toggle('collapsed');
    document.body.classList.toggle('has-sidebar');
    document.body.classList.toggle('sidebar-collapsed');
    toggle.textContent = s.hidden ? '\\u2630' : '\\u2039';
  });

  // --- File icon by extension ---
  function fileIcon(name) {
    var ext = name.split('.').pop().toLowerCase();
    var icons = {
      md: '\\uD83D\\uDCC4', html: '\\uD83C\\uDF10', htm: '\\uD83C\\uDF10',
      pdf: '\\uD83D\\uDCC1', json: '{ }', yaml: '\\u2699', yml: '\\u2699',
      py: '\\uD83D\\uDC0D', js: 'JS', ts: 'TS', css: '\\uD83C\\uDFA8',
      txt: '\\uD83D\\uDCC3', log: '\\uD83D\\uDCC3', xml: '\\u2702',
      csv: '\\uD83D\\uDCCA', toml: '\\u2699', svg: '\\uD83D\\uDDBC',
    };
    return icons[ext] || '\\uD83D\\uDCC4';
  }

  // --- Render tree ---
  function renderTree(items, depth) {
    var s = getState();
    var html = '';
    items.forEach(function(item) {
      var pad = 'padding-left:' + (12 + depth * 16) + 'px;';
      if (item.type === 'dir') {
        var dirKey = 'dir:' + item.path;
        var isOpen = s[dirKey] !== false;
        html += '<div class="sidebar-dir' + (isOpen ? ' open' : '') + '" style="' + pad + '" data-dir="' + item.path + '">'
          + esc(item.name) + '</div>';
        html += '<div class="sidebar-children' + (isOpen ? '' : ' collapsed') + '" data-dir-children="' + item.path + '">';
        html += renderTree(item.children || [], depth + 1);
        html += '</div>';
      } else {
        var isActive = item.path === currentPath;
        html += '<a class="sidebar-file' + (isActive ? ' active' : '') + '" href="/' + encodeURI(item.path) + '" style="' + pad + '">'
          + '<span class="sidebar-icon">' + fileIcon(item.name) + '</span>'
          + esc(item.name) + '</a>';
      }
    });
    return html;
  }

  function esc(s) {
    var d = document.createElement('div');
    d.textContent = s;
    return d.innerHTML;
  }

  // --- Fetch and render ---
  fetch('/api/files').then(function(r) { return r.json(); }).then(function(data) {
    tree.innerHTML = renderTree(data.files || [], 0);

    // Directory toggle
    tree.addEventListener('click', function(e) {
      var dir = e.target.closest('.sidebar-dir');
      if (!dir) return;
      var key = 'dir:' + dir.getAttribute('data-dir');
      var children = tree.querySelector('[data-dir-children="' + dir.getAttribute('data-dir') + '"]');
      if (!children) return;
      dir.classList.toggle('open');
      children.classList.toggle('collapsed');
      var s = getState();
      s[key] = dir.classList.contains('open');
      saveState(s);
    });

    // Scroll active file into view
    var active = tree.querySelector('.sidebar-file.active');
    if (active) active.scrollIntoView({ block: 'center' });
  });
})();"""

_COMMENT_HTML = """\
<button id="comment-btn" style="display:none">Comment</button>
<div id="comment-badge" class="comment-count-badge" style="display:none"></div>
<div id="comment-panel" class="comment-panel">
  <div class="comment-panel-header">
    <h3>Comments</h3>
    <button class="comment-panel-close" id="panel-close">&times;</button>
  </div>
  <div class="comment-panel-body" id="panel-body"></div>
</div>
"""


def _sidebar_html(dir_name: str, current_path: str) -> str:
    """Build the sidebar HTML structure and its JS bootstrap."""
    return (
        f'<script>window.__servePath = {_js_string(current_path)};</script>\n'
        '<nav id="serve-sidebar">'
        f'<div id="serve-sidebar-header"><span class="dir-name">{_html_escape(dir_name)}</span></div>'
        '<div id="serve-sidebar-tree"></div>'
        '</nav>'
        '<button id="serve-sidebar-toggle">&lsaquo;</button>\n'
    )


def _js_string(s: str) -> str:
    """Encode a Python string as a JS string literal."""
    return '"' + s.replace("\\", "\\\\").replace('"', '\\"').replace("\n", "\\n") + '"'


def _html_escape(s: str) -> str:
    return s.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;").replace('"', "&quot;")


def wrap_markdown(
    title: str,
    content: str,
    pygments_css: str,
    *,
    sidebar: tuple[str, str] | None = None,
    favicon_path: str = "",
) -> str:
    """Wrap rendered markdown HTML in a full HTML document with comment support.

    sidebar: optional (dir_name, current_path) to inject file navigation.
    favicon_path: path string used to deterministically pick a favicon.
    """
    parts = [_HEAD_TEMPLATE.format(title=title, pygments_css=pygments_css, favicon=_favicon_link(favicon_path))]
    parts.append(_COMMENT_CSS)
    if sidebar:
        parts.append(_SIDEBAR_CSS)
    parts.append(_HEAD_CLOSE)
    if sidebar:
        parts.append(_sidebar_html(*sidebar))
    parts.append(content)
    parts.append(_COMMENT_HTML)
    parts.append("<script>" + RELOAD_SCRIPT + "</script>\n")
    parts.append("<script>" + _COMMENT_JS + "</script>\n")
    if sidebar:
        parts.append("<script>" + _SIDEBAR_JS + "</script>\n")
    parts.append(_BODY_CLOSE)
    return "".join(parts)


def inject_reload_script(
    html: str,
    *,
    sidebar: tuple[str, str] | None = None,
    favicon_path: str = "",
) -> str:
    """Inject the live-reload and comment scripts into an existing HTML document.

    sidebar: optional (dir_name, current_path) to inject file navigation.
    favicon_path: path string used to deterministically pick a favicon.
    """
    favicon_tag = _favicon_link(favicon_path)

    css_parts = [_COMMENT_CSS]
    if sidebar:
        css_parts.append(_SIDEBAR_CSS)
    css_tag = "<style>" + "\n".join(css_parts) + "</style>"

    script_parts = []
    if sidebar:
        script_parts.append(_sidebar_html(*sidebar))
    script_parts.append(_COMMENT_HTML)
    script_parts.append("<script>" + RELOAD_SCRIPT + "</script>\n")
    script_parts.append("<script>" + _COMMENT_JS + "</script>")
    if sidebar:
        script_parts.append("<script>" + _SIDEBAR_JS + "</script>")
    scripts = "\n".join(script_parts)

    # Inject favicon into <head>
    if "</head>" in html:
        html = html.replace("</head>", f"{favicon_tag}</head>", 1)

    if "</body>" in html:
        html = html.replace("</body>", f"{css_tag}\n{scripts}\n</body>", 1)
    elif "</html>" in html:
        html = html.replace("</html>", f"{css_tag}\n{scripts}\n</html>", 1)
    else:
        html = html + f"\n{css_tag}\n{scripts}"
    return html


def wrap_code(
    title: str,
    highlighted_html: str,
    pygments_css: str,
    *,
    sidebar: tuple[str, str] | None = None,
    favicon_path: str = "",
) -> str:
    """Wrap syntax-highlighted code in a full HTML document."""
    parts = [_HEAD_TEMPLATE.format(title=title, pygments_css=pygments_css, favicon=_favicon_link(favicon_path))]
    if sidebar:
        parts.append(_SIDEBAR_CSS)
    parts.append(_HEAD_CLOSE)
    if sidebar:
        parts.append(_sidebar_html(*sidebar))
    parts.append(highlighted_html)
    parts.append("<script>" + RELOAD_SCRIPT + "</script>\n")
    if sidebar:
        parts.append("<script>" + _SIDEBAR_JS + "</script>\n")
    parts.append(_BODY_CLOSE)
    return "".join(parts)


def wrap_plain(
    title: str,
    text: str,
    *,
    sidebar: tuple[str, str] | None = None,
    favicon_path: str = "",
) -> str:
    """Wrap plain text in a full HTML document."""
    escaped = _html_escape(text)
    parts = [_HEAD_TEMPLATE.format(title=title, pygments_css="", favicon=_favicon_link(favicon_path))]
    if sidebar:
        parts.append(_SIDEBAR_CSS)
    parts.append(_HEAD_CLOSE)
    if sidebar:
        parts.append(_sidebar_html(*sidebar))
    parts.append(f'<pre style="white-space:pre-wrap;word-break:break-word;">{escaped}</pre>')
    parts.append("<script>" + RELOAD_SCRIPT + "</script>\n")
    if sidebar:
        parts.append("<script>" + _SIDEBAR_JS + "</script>\n")
    parts.append(_BODY_CLOSE)
    return "".join(parts)


def wrap_pdf(
    title: str,
    pdf_url: str,
    *,
    sidebar: tuple[str, str] | None = None,
    favicon_path: str = "",
) -> str:
    """Wrap a PDF embed in a full HTML document."""
    parts = [_HEAD_TEMPLATE.format(title=title, pygments_css="", favicon=_favicon_link(favicon_path))]
    if sidebar:
        parts.append(_SIDEBAR_CSS)
    parts.append("""
    body { margin: 0; padding: 0; }
    body.has-sidebar { margin-left: 260px; }
    body.sidebar-collapsed { margin-left: 0; }
    embed { width: 100%; height: 100vh; border: none; }
""")
    parts.append(_HEAD_CLOSE)
    if sidebar:
        parts.append(_sidebar_html(*sidebar))
    parts.append(f'<embed src="/{_html_escape(pdf_url)}?raw=1" type="application/pdf">')
    parts.append("<script>" + RELOAD_SCRIPT + "</script>\n")
    if sidebar:
        parts.append("<script>" + _SIDEBAR_JS + "</script>\n")
    parts.append(_BODY_CLOSE)
    return "".join(parts)


def wrap_image(
    title: str,
    image_url: str,
    *,
    sidebar: tuple[str, str] | None = None,
    favicon_path: str = "",
) -> str:
    """Wrap an image in a full HTML document with optional sidebar."""
    parts = [_HEAD_TEMPLATE.format(title=title, pygments_css="", favicon=_favicon_link(favicon_path))]
    if sidebar:
        parts.append(_SIDEBAR_CSS)
    parts.append("""
    img.serve-image {
      max-width: 100%;
      height: auto;
      display: block;
      margin: 1em auto;
      border-radius: 4px;
      box-shadow: 0 2px 8px rgba(0,0,0,0.1);
    }
""")
    parts.append(_HEAD_CLOSE)
    if sidebar:
        parts.append(_sidebar_html(*sidebar))
    parts.append(f'<img class="serve-image" src="/{_html_escape(image_url)}?raw=1" alt="{_html_escape(title)}">')
    parts.append("<script>" + RELOAD_SCRIPT + "</script>\n")
    if sidebar:
        parts.append("<script>" + _SIDEBAR_JS + "</script>\n")
    parts.append(_BODY_CLOSE)
    return "".join(parts)


def _format_size(size: int) -> str:
    """Format a byte count as a human-readable string."""
    for unit in ("B", "KB", "MB", "GB"):
        if size < 1024:
            return f"{size:.0f} {unit}" if unit == "B" else f"{size:.1f} {unit}"
        size /= 1024
    return f"{size:.1f} TB"


def wrap_file_info(
    title: str,
    file_url: str,
    size: int,
    *,
    sidebar: tuple[str, str] | None = None,
    favicon_path: str = "",
) -> str:
    """Wrap a non-renderable file in a page with download link and sidebar."""
    ext = title.rsplit(".", 1)[-1].upper() if "." in title else "FILE"
    human_size = _format_size(size)
    parts = [_HEAD_TEMPLATE.format(title=title, pygments_css="", favicon=_favicon_link(favicon_path))]
    if sidebar:
        parts.append(_SIDEBAR_CSS)
    parts.append("""
    .file-info {
      text-align: center;
      padding: 4em 2em;
    }
    .file-info .icon {
      font-size: 64px;
      margin-bottom: 0.5em;
    }
    .file-info h2 {
      border-bottom: none;
      margin-bottom: 0.25em;
    }
    .file-info .meta {
      color: #656d76;
      font-size: 14px;
      margin-bottom: 1.5em;
    }
    .file-info .actions {
      display: flex;
      gap: 12px;
      justify-content: center;
    }
    .file-info .actions a {
      display: inline-block;
      padding: 8px 20px;
      border-radius: 6px;
      font-size: 14px;
      font-weight: 500;
      text-decoration: none;
    }
    .file-info .btn-download {
      background: #0078d4;
      color: #fff;
    }
    .file-info .btn-download:hover { background: #106ebe; text-decoration: none; }
    .file-info .btn-open {
      background: #f0f0f0;
      color: #24292e;
    }
    .file-info .btn-open:hover { background: #e0e0e0; text-decoration: none; }
""")
    parts.append(_HEAD_CLOSE)
    if sidebar:
        parts.append(_sidebar_html(*sidebar))
    raw_url = f"/{_html_escape(file_url)}?raw=1"
    parts.append(
        f'<div class="file-info">'
        f'<div class="icon">&#128196;</div>'
        f'<h2>{_html_escape(title)}</h2>'
        f'<div class="meta">{_html_escape(ext)} &middot; {human_size}</div>'
        f'<div class="actions">'
        f'<a class="btn-download" href="{raw_url}" download="{_html_escape(title)}">Download</a>'
        f'<a class="btn-open" href="{raw_url}" target="_blank">Open in new tab</a>'
        f'</div></div>'
    )
    parts.append("<script>" + RELOAD_SCRIPT + "</script>\n")
    if sidebar:
        parts.append("<script>" + _SIDEBAR_JS + "</script>\n")
    parts.append(_BODY_CLOSE)
    return "".join(parts)
