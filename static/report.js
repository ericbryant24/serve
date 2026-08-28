(function () {
  'use strict';

  // -------------------------------------------------------------------------
  // State
  // -------------------------------------------------------------------------

  var state = {
    step: 'compose',
    kind: 'bug',
    capture: null,       // { blob, mode, note }
    captureError: '',
    repro: '',
    reportId: '',
    review: null,
    acked: false,
    busy: false,
    message: null,       // { kind: 'err'|'ok'|'info', html: '...' }
    device: null         // { user_code, verification_uri, device_code, interval, expires_in }
  };

  var modal, titleEl, stepEl, bodyEl, footEl;

  // -------------------------------------------------------------------------
  // Capture
  //
  // The page is cloned, then the clone is walked in lockstep with the original
  // so computed styles and box sizes can be read from the live tree (a
  // detached clone reports none). In structural mode every run of text is
  // wrapped in a span with transparent text over a solid ground: the glyphs
  // stay in place and keep driving layout, so metrics, wrapping and overflow
  // are identical while the words are unreadable.
  // -------------------------------------------------------------------------

  var SKIP_TAGS = { SCRIPT: 1, STYLE: 1, TITLE: 1, NOSCRIPT: 1, TEMPLATE: 1 };
  var HATCH = 'repeating-linear-gradient(45deg,#c9ced6 0,#c9ced6 6px,#e4e7ec 6px,#e4e7ec 12px)';

  function redactText(origRoot, cloneRoot) {
    var wo = document.createTreeWalker(origRoot, NodeFilter.SHOW_TEXT, null, false);
    var wc = document.createTreeWalker(cloneRoot, NodeFilter.SHOW_TEXT, null, false);
    var jobs = [], o, c;

    // Collect the whole worklist before mutating: editing the clone during the
    // walk would desync the two walkers.
    while ((o = wo.nextNode()) !== null && (c = wc.nextNode()) !== null) {
      var p = o.parentElement;
      if (!p || SKIP_TAGS[p.tagName]) continue;
      if (!o.nodeValue || !o.nodeValue.trim()) continue;
      var color = '#3a3a3a';
      try {
        var cs = window.getComputedStyle(p);
        if (cs && cs.color && cs.color !== 'rgba(0, 0, 0, 0)') color = cs.color;
      } catch (e) { /* keep the fallback */ }
      jobs.push([c, color]);
    }

    for (var i = 0; i < jobs.length; i++) {
      var node = jobs[i][0];
      if (!node.parentNode) continue;
      var span = document.createElement('span');
      span.setAttribute('style',
        'color:transparent;background:' + jobs[i][1] + ';border-radius:2px;' +
        '-webkit-box-decoration-break:clone;box-decoration-break:clone;');
      node.parentNode.replaceChild(span, node);
      span.appendChild(node);
    }
  }

  function blankMedia(origRoot, cloneRoot, selector) {
    var os = origRoot.querySelectorAll(selector);
    var cs = cloneRoot.querySelectorAll(selector);
    var n = Math.min(os.length, cs.length);
    for (var i = 0; i < n; i++) {
      var node = cs[i];
      if (!node.parentNode) continue; // an ancestor was already replaced
      var w = 0, h = 0;
      try {
        var r = os[i].getBoundingClientRect();
        w = Math.round(r.width); h = Math.round(r.height);
      } catch (e) { /* fall through to the minimum */ }
      var ph = document.createElement('div');
      ph.setAttribute('style',
        'display:inline-block;width:' + Math.max(w, 8) + 'px;height:' + Math.max(h, 8) + 'px;' +
        'border:1px solid #b6bcc6;border-radius:3px;background:' + HATCH + ';');
      node.parentNode.replaceChild(ph, node);
    }
  }

  function blobToDataURL(blob) {
    return new Promise(function (resolve, reject) {
      var fr = new FileReader();
      fr.onload = function () { resolve(fr.result); };
      fr.onerror = function () { reject(new Error('could not read the image')); };
      fr.readAsDataURL(blob);
    });
  }

  // inlineImages is only used by full-fidelity capture. An SVG loaded into an
  // <img> cannot fetch anything, so an image that is not inlined would not
  // render at all; one that fails becomes a placeholder and is counted, so the
  // capture never silently loses content without saying so.
  function inlineImages(origRoot, cloneRoot) {
    var os = origRoot.querySelectorAll('img');
    var cs = cloneRoot.querySelectorAll('img');
    var n = Math.min(os.length, cs.length);
    var omitted = 0, jobs = [];

    for (var i = 0; i < n; i++) {
      (function (orig, clone) {
        var src = orig.currentSrc || orig.src;
        if (!src || src.indexOf('data:') === 0) return;
        jobs.push(
          fetch(src, { credentials: 'same-origin' })
            .then(function (r) { if (!r.ok) throw new Error('http ' + r.status); return r.blob(); })
            .then(blobToDataURL)
            .then(function (durl) { clone.setAttribute('src', durl); })
            .catch(function () {
              omitted++;
              if (!clone.parentNode) return;
              var w = 0, h = 0;
              try { var r = orig.getBoundingClientRect(); w = Math.round(r.width); h = Math.round(r.height); } catch (e) {}
              var ph = document.createElement('div');
              ph.setAttribute('style',
                'display:inline-block;width:' + Math.max(w, 8) + 'px;height:' + Math.max(h, 8) + 'px;' +
                'border:1px solid #b6bcc6;border-radius:3px;background:' + HATCH + ';');
              clone.parentNode.replaceChild(ph, clone);
            })
        );
      })(os[i], cs[i]);
    }
    return Promise.all(jobs).then(function () { return omitted; });
  }

  function pageBackground() {
    try {
      var bg = window.getComputedStyle(document.body).backgroundColor;
      if (bg && bg !== 'rgba(0, 0, 0, 0)' && bg !== 'transparent') return bg;
    } catch (e) { /* fall through */ }
    return '#ffffff';
  }

  function rasterize(clone, vw, vh) {
    return new Promise(function (resolve, reject) {
      // Scripts are stripped: they cannot run inside an <img>-loaded SVG, they
      // would need XML escaping, and serve inlines every asset so they are the
      // bulk of the page weight.
      var strip = clone.querySelectorAll('script, link[rel="stylesheet"]');
      for (var i = 0; i < strip.length; i++) {
        if (strip[i].parentNode) strip[i].parentNode.removeChild(strip[i]);
      }

      var xml;
      try {
        xml = new XMLSerializer().serializeToString(clone);
      } catch (e) {
        reject(new Error('the page could not be serialized'));
        return;
      }

      var svg =
        '<svg xmlns="http://www.w3.org/2000/svg" width="' + vw + '" height="' + vh + '">' +
        '<foreignObject x="0" y="0" width="100%" height="100%">' + xml +
        '</foreignObject></svg>';

      var img = new Image();
      var scale = Math.min(window.devicePixelRatio || 1, 2);
      var settled = false;
      var timer = setTimeout(function () {
        if (settled) return;
        settled = true;
        reject(new Error('the capture timed out'));
      }, 8000);

      img.onload = function () {
        if (settled) return;
        settled = true;
        clearTimeout(timer);
        try {
          var canvas = document.createElement('canvas');
          canvas.width = Math.max(Math.round(vw * scale), 1);
          canvas.height = Math.max(Math.round(vh * scale), 1);
          var ctx = canvas.getContext('2d');
          ctx.fillStyle = pageBackground();
          ctx.fillRect(0, 0, canvas.width, canvas.height);
          ctx.scale(scale, scale);
          ctx.drawImage(img, 0, 0);
          if (!canvas.toBlob) { reject(new Error('this browser cannot export the capture')); return; }
          canvas.toBlob(function (b) {
            if (b) resolve(b); else reject(new Error('the capture produced no image'));
          }, 'image/png');
        } catch (e) {
          reject(e instanceof Error ? e : new Error('the capture failed'));
        }
      };
      img.onerror = function () {
        if (settled) return;
        settled = true;
        clearTimeout(timer);
        reject(new Error('the page could not be rasterized'));
      };
      img.src = 'data:image/svg+xml;charset=utf-8,' + encodeURIComponent(svg);
    });
  }

  function capturePage(structural) {
    var docEl = document.documentElement;
    var vw = docEl.clientWidth || window.innerWidth;
    var vh = window.innerHeight;
    var sx = window.scrollX || 0;
    var sy = window.scrollY || 0;

    var clone = docEl.cloneNode(true);
    var prep;

    if (structural) {
      redactText(docEl, clone);
      blankMedia(docEl, clone, 'img,svg,canvas,video,iframe,embed,object');
      prep = Promise.resolve(0);
    } else {
      // Inline SVG serializes and renders fine, so it survives full capture.
      blankMedia(docEl, clone, 'canvas,video,iframe,embed,object');
      prep = inlineImages(docEl, clone);
    }

    return prep.then(function (omitted) {
      // Remove our own UI last: doing it earlier would desync the lockstep walks.
      var mine = clone.querySelectorAll('#serve-report-modal, #serve-report-btn');
      for (var i = 0; i < mine.length; i++) {
        if (mine[i].parentNode) mine[i].parentNode.removeChild(mine[i]);
      }

      // Offset so the drawn region is the viewport the reporter is looking at.
      var b = clone.querySelector('body');
      if (b) {
        b.style.position = 'relative';
        b.style.top = (-sy) + 'px';
        b.style.left = (-sx) + 'px';
      }
      clone.style.width = vw + 'px';

      return rasterize(clone, vw, vh).then(function (blob) {
        return {
          blob: blob,
          mode: structural ? 'structural' : 'full',
          note: omitted ? omitted + ' image(s) could not be inlined and were blanked' : ''
        };
      });
    });
  }

  // -------------------------------------------------------------------------
  // Page context
  // -------------------------------------------------------------------------

  // A markdown document containing a fenced code block also contains
  // .highlight, so presence alone cannot distinguish the two. A code view is
  // wrapCode's output: the highlighted block is the ONLY child of the content
  // container.
  function viewKind() {
    if (document.querySelector('embed[type="application/pdf"]')) return 'pdf';
    if (document.querySelector('img.serve-image')) return 'image';
    if (document.body.classList.contains('marp') || document.querySelector('.marp-slide')) return 'marp';
    var content = document.getElementById('serve-content');
    if (content && content.children.length === 1) {
      var only = content.firstElementChild;
      if (only.matches('.highlight, pre.chroma')) return 'code';
      if (only.matches('pre')) return 'plain';
    }
    return 'markdown';
  }

  // selectionRepro returns the source lines behind the current selection,
  // reusing the data-source-lines annotations the renderer already emits. For a
  // rendering bug this is a better artifact than an image: small, reviewable,
  // and usually not sensitive.
  function selectionRepro() {
    var sel = window.getSelection();
    if (!sel || sel.isCollapsed || !sel.toString().trim()) return '';
    var node = sel.getRangeAt(0).commonAncestorContainer;
    if (node.nodeType === 3) node = node.parentElement;
    var block = node && node.closest ? node.closest('[data-source-lines]') : null;
    if (!block) return '';
    var lines = block.getAttribute('data-source-lines') || '';
    return 'Source lines ' + lines + ':\n\n```\n' + block.textContent.trim() + '\n```\n';
  }

  // -------------------------------------------------------------------------
  // API
  // -------------------------------------------------------------------------

  function jreq(method, path, body) {
    var opts = { method: method, headers: { 'Content-Type': 'application/json' } };
    if (body) opts.body = JSON.stringify(body);
    return fetch(path, opts).then(function (r) {
      return r.text().then(function (t) {
        if (!r.ok) {
          var err = new Error(t.trim() || (r.status + ' ' + r.statusText));
          err.status = r.status;
          throw err;
        }
        return t ? JSON.parse(t) : null;
      });
    });
  }

  function uploadCapture(id, cap) {
    var fd = new FormData();
    fd.append('kind', 'screenshot');
    fd.append('mode', cap.mode);
    fd.append('file', cap.blob, 'screenshot.png');
    return fetch('/api/report/' + id + '/attachment', { method: 'POST', body: fd })
      .then(function (r) {
        return r.text().then(function (t) {
          if (!r.ok) throw new Error(t.trim() || ('upload failed: ' + r.status));
          return JSON.parse(t);
        });
      });
  }

  // -------------------------------------------------------------------------
  // Rendering
  // -------------------------------------------------------------------------

  function esc(s) {
    var d = document.createElement('div');
    d.textContent = s == null ? '' : String(s);
    return d.innerHTML;
  }

  function fmtSize(n) {
    if (n < 1024) return n + ' B';
    if (n < 1024 * 1024) return (n / 1024).toFixed(1) + ' KB';
    return (n / 1024 / 1024).toFixed(1) + ' MB';
  }

  function messageHTML() {
    if (!state.message) return '';
    return '<div class="srp-msg ' + state.message.kind + '">' + state.message.html + '</div>';
  }

  function renderCompose() {
    titleEl.textContent = state.kind === 'feature' ? 'Request a feature' : 'Report a bug';
    stepEl.textContent = 'Step 1 of 2';

    var capLine;
    if (state.kind === 'feature') {
      capLine = 'No screenshot is attached to a feature request.';
    } else if (state.captureError) {
      capLine = 'The screenshot could not be captured (' + esc(state.captureError) +
        '). Everything else still works.';
    } else if (state.capture) {
      capLine = 'A screenshot was captured with all document text replaced by solid bars. ' +
        'You will see it, and choose whether to attach it, on the next step.';
    } else {
      capLine = 'Capturing the page&hellip;';
    }

    bodyEl.innerHTML =
      messageHTML() +
      '<div class="srp-field">' +
        '<label>What kind of report is this?</label>' +
        '<div class="srp-kinds">' +
          '<button type="button" class="srp-kind" data-kind="bug" aria-pressed="' +
            (state.kind === 'bug') + '"><strong>Bug</strong><br>Something behaves wrong</button>' +
          '<button type="button" class="srp-kind" data-kind="feature" aria-pressed="' +
            (state.kind === 'feature') + '"><strong>Feature request</strong><br>Something is missing</button>' +
        '</div>' +
      '</div>' +
      '<div class="srp-field">' +
        '<label for="srp-title">Title</label>' +
        '<input type="text" id="srp-title" maxlength="200" placeholder="' +
          (state.kind === 'feature' ? 'Add a keyboard shortcut for&hellip;' : 'Table overflows the page on&hellip;') +
        '">' +
      '</div>' +
      '<div class="srp-field">' +
        '<label for="srp-body">' +
          (state.kind === 'feature' ? 'What would it do?' : 'What happened?') +
        '</label>' +
        '<textarea id="srp-body" placeholder="' +
          (state.kind === 'feature' ? 'What you want to do, and why it is awkward today.'
                                    : 'What you did, what you expected, what happened instead.') +
        '"></textarea>' +
      '</div>' +
      (state.repro
        ? '<div class="srp-field"><label><input type="checkbox" id="srp-repro" checked> ' +
          'Attach the source of the text you selected</label>' +
          '<div class="srp-hint">A few lines of source, not an image. Usually the most useful thing you can send.</div></div>'
        : '') +
      '<div class="srp-field"><label><input type="checkbox" id="srp-log" ' +
        (state.kind === 'bug' ? 'checked' : '') + '> Attach the recent log</label>' +
        '<div class="srp-hint">The last 500 events. File paths are shortened before they are recorded.</div></div>' +
      '<div class="srp-hint">' + capLine + '</div>';

    footEl.innerHTML =
      '<span class="srp-spacer">Nothing is sent yet.</span>' +
      '<button type="button" class="srp-btn" data-act="close">Cancel</button>' +
      '<button type="button" class="srp-btn primary" data-act="review">Continue to review</button>';
  }

  function attachmentCard(a) {
    var name = a.kind === 'screenshot' ? 'Screenshot' : a.kind === 'log' ? 'Recent log' : 'Source excerpt';
    if (a.mode) name += ' (' + a.mode + ')';

    var preview = '';
    if (a.kind === 'screenshot') {
      preview = '<img src="/api/report/' + encodeURIComponent(state.reportId) +
        '/attachment/' + encodeURIComponent(a.id) + '" alt="Captured page">';
    } else if (a.text) {
      preview = '<pre>' + esc(a.text.length > 4000 ? a.text.slice(0, 4000) + '\n…' : a.text) + '</pre>';
    }

    var warn = '';
    if (a.secrets && a.secrets.length) {
      warn = ' <span style="color:#953800;font-weight:600">' + a.secrets.length + ' flagged</span>';
    }

    return '<div class="srp-att' + (a.included ? ' on' : '') + '">' +
      '<label class="srp-att-head">' +
        '<input type="checkbox" data-att="' + esc(a.id) + '"' + (a.included ? ' checked' : '') + '>' +
        '<span class="srp-att-name">' + esc(name) + '</span>' +
        '<span class="srp-att-meta">' + fmtSize(a.bytes) + warn + '</span>' +
      '</label>' +
      (preview ? '<div class="srp-att-preview">' + preview + '</div>' : '') +
      '</div>';
  }

  function renderReview() {
    var rv = state.review;
    titleEl.textContent = 'Review before sending';
    stepEl.textContent = 'Step 2 of 2';

    var secretsBlock = '';
    if (rv.secret_count > 0) {
      var items = [];
      (rv.body_secrets || []).forEach(function (h) {
        items.push('<li>' + esc(h.kind) + ' &mdash; <code>' + esc(h.excerpt) + '</code> (in the description)</li>');
      });
      (rv.attachments || []).forEach(function (a) {
        if (!a.included || !a.secrets) return;
        a.secrets.forEach(function (h) {
          items.push('<li>' + esc(h.kind) + ' &mdash; <code>' + esc(h.excerpt) + '</code> (in ' + esc(a.kind) + ')</li>');
        });
      });
      secretsBlock =
        '<div class="srp-secrets">' +
          '<h4>' + rv.secret_count + ' item' + (rv.secret_count === 1 ? '' : 's') + ' look like credentials</h4>' +
          '<p>These were found in what you are about to send. Edit them out, turn off the attachment, or confirm they are safe.</p>' +
          '<ul class="srp-secret-list">' + items.join('') + '</ul>' +
          '<label class="srp-ack"><input type="checkbox" id="srp-ack"' + (state.acked ? ' checked' : '') +
            '> I have checked these and they are safe to publish.</label>' +
        '</div>';
    }

    var authNote = (rv.upload_allowed && !rv.authenticated)
      ? '<div class="srp-hint" style="margin-top:6px">First time on this machine: ' +
        'GitHub will ask you to authorize before anything is posted.</div>'
      : '';
    var dest = rv.upload_allowed
      ? '<div class="srp-dest">Filing sends this to <code>' + esc(rv.destination) +
        '</code> as a <span class="srp-public">public issue</span>. ' +
        'Public issues are indexed and copied quickly, so treat it as permanent.' +
        authNote + '</div>'
      : '<div class="srp-dest srp-blocked">' + esc(rv.upload_blocked_reason || 'Filing is disabled.') + '</div>';

    bodyEl.innerHTML =
      messageHTML() +
      '<div class="srp-section-label">Exactly what will be posted</div>' +
      '<div class="srp-payload"><div class="srp-payload-title">' + esc(rv.title) + '</div>' +
        esc(rv.markdown) + '</div>' +
      '<div class="srp-section-label">Attachments &mdash; off unless you turn them on</div>' +
      (rv.attachments && rv.attachments.length
        ? rv.attachments.map(attachmentCard).join('')
        : '<div class="srp-hint">Nothing captured.</div>') +
      secretsBlock +
      dest;

    var blockedBySecret = rv.secret_count > 0 && !state.acked;
    var canFile = rv.upload_allowed && !blockedBySecret && !state.busy;

    footEl.innerHTML =
      '<span class="srp-spacer">Saved at <code>~/.serve/reports/' + esc(state.reportId) + '/</code></span>' +
      '<button type="button" class="srp-btn" data-act="back">Back</button>' +
      '<button type="button" class="srp-btn" data-act="reveal">Open folder</button>' +
      '<button type="button" class="srp-btn primary" data-act="done">Save locally</button>' +
      (rv.upload_allowed
        ? '<button type="button" class="srp-btn" data-act="file"' + (canFile ? '' : ' disabled') +
          '>File on GitHub</button>'
        : '');
  }

  function renderDevice() {
    var d = state.device;
    titleEl.textContent = 'Authorize with GitHub';
    stepEl.textContent = '';
    bodyEl.innerHTML =
      messageHTML() +
      '<p>Enter this code at <a href="' + esc(d.verification_uri) + '" target="_blank" rel="noopener">' +
        esc(d.verification_uri) + '</a>:</p>' +
      '<div class="srp-code-row">' +
        '<div class="srp-code" id="srp-code">' + esc(d.user_code) + '</div>' +
        '<button type="button" class="srp-btn" data-act="copy-code">Copy</button>' +
      '</div>' +
      (state.busy
        ? '<p class="srp-waiting"><span class="srp-spinner" aria-hidden="true"></span>' +
          'Waiting for you to approve on GitHub. This screen will move on by itself.</p>'
        : '') +
      '<p class="srp-hint">The issue is filed as you, on your own GitHub account. ' +
        'This machine is authorized once; later reports are a single click.</p>';
    footEl.innerHTML =
      '<span class="srp-spacer"></span>' +
      '<button type="button" class="srp-btn" data-act="back-review">Cancel</button>' +
      '<a class="srp-btn primary srp-link-btn" href="' + esc(d.verification_uri) +
        '" target="_blank" rel="noopener">Open GitHub</a>';
  }

  function render() {
    if (state.step === 'compose') renderCompose();
    else if (state.step === 'review') renderReview();
    else if (state.step === 'device') renderDevice();
  }

  // -------------------------------------------------------------------------
  // Actions
  // -------------------------------------------------------------------------

  function setMessage(kind, html) { state.message = { kind: kind, html: html }; }

  // Clipboard access can be denied; selecting the code keeps a manual copy
  // one keystroke away.
  function selectCode(el) {
    if (!el) return;
    var r = document.createRange();
    r.selectNodeContents(el);
    var sel = window.getSelection();
    sel.removeAllRanges();
    sel.addRange(r);
  }

  function goReview() {
    var title = (document.getElementById('srp-title') || {}).value || '';
    var body = (document.getElementById('srp-body') || {}).value || '';
    if (!title.trim()) {
      setMessage('err', 'A title is needed so the report is findable.');
      render();
      var t = document.getElementById('srp-title');
      if (t) t.focus();
      return;
    }
    var reproEl = document.getElementById('srp-repro');
    var logEl = document.getElementById('srp-log');

    state.busy = true;
    state.message = null;
    footEl.innerHTML = '<span class="srp-spacer">Saving locally&hellip;</span>';

    jreq('POST', '/api/report', {
      kind: state.kind,
      title: title,
      body: body,
      browser: navigator.userAgent,
      view_kind: viewKind(),
      repro: (reproEl && reproEl.checked) ? state.repro : '',
      with_log: !!(logEl && logEl.checked)
    }).then(function (rv) {
      state.reportId = rv.report.id;
      if (state.kind === 'bug' && state.capture) {
        return uploadCapture(state.reportId, state.capture);
      }
      return rv;
    }).then(function (rv) {
      state.review = rv;
      state.step = 'review';
      state.busy = false;
      if (state.capture && state.capture.note) setMessage('info', esc(state.capture.note));
      else if (state.captureError) setMessage('info', 'No screenshot: ' + esc(state.captureError));
      render();
    }).catch(function (e) {
      state.busy = false;
      setMessage('err', esc(e.message || 'The report could not be saved.'));
      render();
    });
  }

  function toggleAttachment(aid, on) {
    jreq('PATCH', '/api/report/' + encodeURIComponent(state.reportId), {
      include: aid, included: on
    }).then(function (rv) {
      state.review = rv;
      render();
    }).catch(function (e) {
      setMessage('err', esc(e.message || 'Could not update the attachment.'));
      render();
    });
  }

  function fileIt(deviceCode) {
    state.busy = true;
    state.message = null;
    render();

    var payload = {};
    if (deviceCode && state.device) {
      payload = {
        device_code: state.device.device_code,
        interval: state.device.interval,
        expires_in: state.device.expires_in
      };
    }

    jreq('POST', '/api/report/' + encodeURIComponent(state.reportId) + '/file', payload)
      .then(function (res) {
        state.busy = false;
        if (res.status === 'authorize') {
          state.device = res;
          state.step = 'device';
          render();
          // Start waiting immediately. Every other device flow polls on its
          // own; making the reporter come back and press a button means an
          // approval can land with nothing happening.
          fileIt(true);
          return;
        }
        state.step = 'review';
        setMessage('ok',
          'Filed. <a href="' + esc(res.issue_url) + '" target="_blank" rel="noopener">Open the issue</a>. ' +
          'To add the screenshot, open the report folder and drag the PNG into the issue.');
        return jreq('GET', '/api/report/' + encodeURIComponent(state.reportId)).then(function (rv) {
          state.review = rv;
          render();
        });
      })
      .catch(function (e) {
        state.busy = false;
        state.step = 'review';
        setMessage('err', esc(e.message || 'Filing failed.'));
        render();
      });
  }

  // -------------------------------------------------------------------------
  // Modal plumbing
  // -------------------------------------------------------------------------

  function close() {
    modal.hidden = true;
    document.removeEventListener('keydown', onKey, true);
    state.step = 'compose';
    state.review = null;
    state.reportId = '';
    state.capture = null;
    state.captureError = '';
    state.message = null;
    state.device = null;
    state.acked = false;
    state.busy = false;
  }

  function onKey(e) {
    if (e.key === 'Escape' && !state.busy) { e.stopPropagation(); close(); }
  }

  function onClick(e) {
    var kindBtn = e.target.closest ? e.target.closest('.srp-kind') : null;
    if (kindBtn) {
      state.kind = kindBtn.getAttribute('data-kind');
      var title = (document.getElementById('srp-title') || {}).value || '';
      var body = (document.getElementById('srp-body') || {}).value || '';
      render();
      var t = document.getElementById('srp-title'); if (t) t.value = title;
      var b = document.getElementById('srp-body'); if (b) b.value = body;
      return;
    }

    var act = e.target.getAttribute ? e.target.getAttribute('data-act') : null;
    if (!act) return;

    if (act === 'close') { close(); return; }
    if (act === 'review') { goReview(); return; }
    if (act === 'back') { state.step = 'compose'; state.message = null; render(); return; }
    if (act === 'back-review') { state.step = 'review'; state.device = null; state.message = null; render(); return; }
    if (act === 'done') {
      var id = state.reportId;
      close();
      toast('Saved locally. Export it with: serve report export ' + id);
      return;
    }
    if (act === 'reveal') {
      jreq('POST', '/api/report/' + encodeURIComponent(state.reportId) + '/reveal', {})
        .catch(function (err) { setMessage('err', esc(err.message)); render(); });
      return;
    }
    if (act === 'copy-code') {
      var codeEl = document.getElementById('srp-code');
      var code = codeEl ? codeEl.textContent.trim() : '';
      var done = function () {
        e.target.textContent = 'Copied';
        setTimeout(function () { if (e.target) e.target.textContent = 'Copy'; }, 2000);
      };
      if (navigator.clipboard && navigator.clipboard.writeText) {
        navigator.clipboard.writeText(code).then(done, function () { selectCode(codeEl); });
      } else {
        selectCode(codeEl);
      }
      return;
    }
    if (act === 'file') { fileIt(false); return; }
    if (act === 'await') { fileIt(true); return; }
  }

  function onChange(e) {
    var t = e.target;
    if (t.id === 'srp-ack') { state.acked = t.checked; render(); return; }
    var aid = t.getAttribute ? t.getAttribute('data-att') : null;
    if (aid) { toggleAttachment(aid, t.checked); return; }
  }

  function toast(text) {
    var el = document.createElement('div');
    el.className = 'serve-report-toast';
    el.setAttribute('style',
      'position:fixed;bottom:20px;left:50%;transform:translateX(-50%);z-index:2100;' +
      'background:#24292e;color:#fff;padding:10px 16px;border-radius:6px;font-size:13px;' +
      'font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Helvetica,Arial,sans-serif;' +
      'box-shadow:0 2px 12px rgba(0,0,0,0.25);max-width:80vw;');
    el.textContent = text;
    document.body.appendChild(el);
    setTimeout(function () { if (el.parentNode) el.parentNode.removeChild(el); }, 6000);
  }

  function open() {
    state.repro = selectionRepro();
    state.step = 'compose';
    state.capture = null;
    state.captureError = '';
    modal.hidden = false;
    document.addEventListener('keydown', onKey, true);
    render();

    // Capture before anything else is drawn, so the modal never lands in its
    // own screenshot.
    capturePage(true).then(function (cap) {
      state.capture = cap;
      if (state.step === 'compose') render();
    }).catch(function (e) {
      state.captureError = e.message || 'capture failed';
      if (state.step === 'compose') render();
    });

    var t = document.getElementById('srp-title');
    if (t) t.focus();
  }

  function init() {
    modal = document.getElementById('serve-report-modal');
    var btn = document.getElementById('serve-report-btn');
    if (!modal || !btn) return;

    titleEl = modal.querySelector('.srp-title');
    stepEl = modal.querySelector('.srp-step');
    bodyEl = modal.querySelector('.srp-body');
    footEl = modal.querySelector('.srp-foot');

    btn.addEventListener('click', open);
    modal.addEventListener('click', function (e) {
      if (e.target === modal && !state.busy) { close(); return; }
      onClick(e);
    });
    modal.addEventListener('change', onChange);
    var closeBtn = modal.querySelector('.srp-close');
    if (closeBtn) closeBtn.addEventListener('click', function () { if (!state.busy) close(); });
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
