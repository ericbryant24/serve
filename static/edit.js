(function() {
  var editing = false;
  var splitMode = false;
  var previewTimer = null;
  var originalContent = '';

  var editBtn, editor, closeBtn, cancelBtn, splitToggle, textarea, preview, contentEl;

  function fileParam() {
    if (typeof window.__servePath === 'string' && window.__servePath) {
      return '?file=' + encodeURIComponent(window.__servePath);
    }
    return '';
  }

  function fetchFileContent(cb) {
    fetch('/api/file' + fileParam())
      .then(function(r) { return r.json(); })
      .then(function(d) { cb(d.content); })
      .catch(function() { cb(''); });
  }

  function saveFileContent(content, cb) {
    fetch('/api/edit' + fileParam(), {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({content: content})
    })
      .then(function(r) { return r.json(); })
      .then(function(d) { if (cb) cb(d); })
      .catch(function() { if (cb) cb(null); });
  }

  function updatePreview() {
    if (!splitMode || !preview) return;
    fetch('/api/preview', {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({content: textarea.value})
    })
      .then(function(r) { return r.json(); })
      .then(function(d) {
        preview.innerHTML = d.html;
        syncScrollToPreview();
      })
      .catch(function() {});
  }

  function schedulePreview() {
    clearTimeout(previewTimer);
    previewTimer = setTimeout(updatePreview, 300);
  }

  function syncScrollToPreview() {
    if (!splitMode || !textarea || !preview) return;
    var editorRange = textarea.scrollHeight - textarea.clientHeight;
    if (editorRange <= 0) return;
    var ratio = textarea.scrollTop / editorRange;
    preview.scrollTop = ratio * (preview.scrollHeight - preview.clientHeight);
  }

  function syncRenderedView(done) {
    var sc = document.getElementById('serve-content');
    if (!sc) { if (done) done(); return; }
    var scrollY = window.scrollY;
    fetch(location.href, {cache: 'no-store'})
      .then(function(r) { return r.text(); })
      .then(function(html) {
        try {
          var doc = new DOMParser().parseFromString(html, 'text/html');
          var nc = doc.getElementById('serve-content');
          if (nc) {
            sc.innerHTML = nc.innerHTML;
            window.scrollTo(0, scrollY);
            if (window.mermaid) {
              var mels = sc.querySelectorAll('pre.mermaid:not([data-processed])');
              if (mels.length) mermaid.run({nodes: Array.from(mels)});
            }
            if (window.__refreshComments) window.__refreshComments();
          }
        } catch(e) {}
        if (done) done();
      })
      .catch(function() { if (done) done(); });
  }

  function openEditor() {
    if (editing) return;
    fetchFileContent(function(content) {
      originalContent = content;
      textarea.value = content;
      editing = true;
      window.__serveEditMode = true;
      document.body.classList.add('serve-editing');
      if (contentEl) contentEl.style.display = 'none';
      if (editBtn) editBtn.style.display = 'none';
      editor.style.display = 'flex';
      textarea.focus();
      if (splitMode) updatePreview();
    });
  }

  // Saves and closes
  function closeEditor() {
    if (!editing) return;
    saveFileContent(textarea.value, function() {
      editing = false;
      window.__serveEditMode = false;
      document.body.classList.remove('serve-editing');
      editor.style.display = 'none';
      if (editBtn) editBtn.style.display = '';
      if (contentEl) contentEl.style.display = '';
      syncRenderedView(null);
    });
  }

  // Discards changes and closes
  function cancelEditor() {
    if (!editing) return;
    editing = false;
    window.__serveEditMode = false;
    document.body.classList.remove('serve-editing');
    editor.style.display = 'none';
    if (editBtn) editBtn.style.display = '';
    if (contentEl) contentEl.style.display = '';
    // If content was changed, restore the server-rendered view
    if (textarea.value !== originalContent) {
      syncRenderedView(null);
    }
  }

  function toggleSplit() {
    splitMode = !splitMode;
    if (splitMode) {
      editor.classList.add('serve-editor-split');
      splitToggle.classList.add('active');
      updatePreview();
    } else {
      editor.classList.remove('serve-editor-split');
      splitToggle.classList.remove('active');
    }
  }

  // Called by reload script when __serveEditMode is true.
  window.__serveOnReload = function() {
    if (splitMode) updatePreview();
  };

  document.addEventListener('keydown', function(e) {
    if (!editing) return;
    if ((e.ctrlKey || e.metaKey) && e.key === 's') {
      e.preventDefault();
      saveFileContent(textarea.value, null);
    }
    if (e.key === 'Escape') cancelEditor();
  });

  function init() {
    contentEl     = document.getElementById('serve-content');
    editBtn       = document.getElementById('serve-edit-btn');
    editor        = document.getElementById('serve-editor');
    closeBtn      = document.getElementById('serve-editor-close');
    cancelBtn     = document.getElementById('serve-editor-cancel');
    splitToggle   = document.getElementById('serve-editor-split-toggle');
    textarea      = document.getElementById('serve-editor-textarea');
    preview       = document.getElementById('serve-editor-preview');

    if (!editBtn || !editor) return;

    editBtn.addEventListener('click', openEditor);
    closeBtn.addEventListener('click', closeEditor);
    if (cancelBtn) cancelBtn.addEventListener('click', cancelEditor);
    splitToggle.addEventListener('click', toggleSplit);
    textarea.addEventListener('input', schedulePreview);
    textarea.addEventListener('scroll', syncScrollToPreview);
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
