// Earl Scheib Concord — Queue Admin UI
// Vanilla JS. No framework, no bundler.
//
// Two serving modes:
//   1. Local Go proxy (Marco's `earlscheib.exe --admin`): fetches /api/queue,
//      /api/cancel, /api/send-now, /api/diagnostic, /alive. This is the
//      default when window.API_BASE_PATH is undefined.
//   2. Public app.py at /earlscheib (operator-only, basic-auth'd): index.html
//      injects `<script>window.API_BASE_PATH = "/earlscheibconcord"</script>`
//      before this file loads. Fetches /earlscheibconcord/queue,
//      /earlscheibconcord/queue (DELETE), /earlscheibconcord/queue/send-now,
//      /earlscheibconcord/diagnostic. No /alive — app.py has no local
//      watchdog to heartbeat.
//
// The API_BASE_PATH injection lives only in the served HTML (app.py wraps
// ui_public/index.html with an inline script tag); this file itself is
// byte-identical across internal/admin/ui/ and ui_public/ — keep it that
// way via `make sync-ui`.

(function () {
  'use strict';

  // When undefined, fall back to the Go admin's local proxy base.
  const API_BASE = (typeof window !== 'undefined' && window.API_BASE_PATH)
    ? window.API_BASE_PATH
    : '/api';
  const IS_LOCAL_ADMIN = API_BASE === '/api';

  const REFRESH_MS   = 15000;
  const HEARTBEAT_MS = 10000;
  const DIAG_MS      = 5000;
  // OH4 OVERRIDE 2: when a poll fails, retry every 3s for 60s before
  // showing the "sleep" panel. Any successful poll in the retry window
  // dismisses the panel and resumes normal cadence.
  const RETRY_MS    = 3000;
  const RETRY_LIMIT = 20; // 20 * 3s = 60s retry window
  // QAJ-02: debounce for search input.
  const SEARCH_DEBOUNCE_MS = 150;

  // SMS templates — MUST stay byte-for-byte identical to app.py MSG_*.
  // If you change the app.py templates, update the three strings below.
  // app.py location: constants MSG_24H / MSG_3DAY / MSG_REVIEW near line 96.
  const SMS_TEMPLATES = {
    '24h':
      'Hi {name}, this is Earl Scheib Auto Body in Concord. Just following up on your recent estimate. ' +
      'Have questions or ready to schedule? Call us at (925) 609-7780.',
    '3day':
      'Hi {name}, Earl Scheib Auto Body Concord checking in about your estimate from a few days ago. ' +
      "We'd love to help get your car looking great! Call (925) 609-7780.",
    'review':
      'Hi {name}, thank you for choosing Earl Scheib Auto Body Concord! Hope you\'re happy with your repair. ' +
      'Would you mind leaving us a Google review? It means a lot: https://g.page/r/review',
  };

  function previewSMS(jobType, name) {
    const tpl = SMS_TEMPLATES[jobType] || '';
    return tpl.replace('{name}', name || 'there');
  }

  const JOB_TYPE_LABELS = {
    '24h':    '24-hour follow-up',
    '3day':   '3-day check-in',
    'review': 'Review request',
  };

  // Pacific-time formatter for the absolute-time chip: "5:28 PM"
  const timeFmt = new Intl.DateTimeFormat('en-US', {
    timeZone: 'America/Los_Angeles',
    hour: 'numeric',
    minute: '2-digit',
  });

  // Session-local counters — no server history endpoint exists yet.
  // Pending is recomputed on every /api/queue success; sent/failed
  // increment only from UI-initiated actions since page load.
  const counters = {
    pending:    0,
    sentToday:  0,
    failed:     0,
  };

  const queueEl        = document.getElementById('queue');
  const syncDotEl      = document.getElementById('sync-dot');
  const syncCaptionEl  = document.getElementById('sync-caption');
  const refreshBtn     = document.getElementById('refresh-btn');
  const statPendingEl  = document.getElementById('stat-pending');
  const statSentEl     = document.getElementById('stat-sent');
  const statFailedEl   = document.getElementById('stat-failed');
  const estimateTpl    = document.getElementById('estimate-card-template');
  const entryTpl       = document.getElementById('timeline-entry-template');
  const emptyTpl       = document.getElementById('empty-state-template');
  const errorTpl       = document.getElementById('error-state-template');
  const sleepTpl       = document.getElementById('sleep-state-template');
  const searchEl       = document.getElementById('search');

  let lastFetchedAt = 0;
  let retryAttempt  = 0;
  let inRetryMode   = false;
  let retryTimerId  = null;

  // QAJ-02 filter + search state. Re-applied against the cached `lastJobs`
  // array on chip click or search keystroke — avoids a network round-trip.
  let currentFilter = 'all';
  let currentSearch = '';
  let lastJobs      = [];
  let searchTimerId = null;

  // ---------- Helpers --------------------------------------------------

  function maskVIN(vin) {
    if (!vin) return '';
    if (vin.length < 6) return vin;
    return 'VIN · ' + vin.slice(-6);
  }

  function formatPhone(raw) {
    if (!raw) return '';
    const d = raw.replace(/[^\d]/g, '');
    if (d.length === 11 && d.startsWith('1')) {
      return `(${d.slice(1, 4)}) ${d.slice(4, 7)}-${d.slice(7)}`;
    }
    return raw;
  }

  function formatAbsolute(unixSeconds) {
    if (!unixSeconds) return '';
    return timeFmt.format(new Date(unixSeconds * 1000));
  }

  function formatRelative(unixSeconds) {
    if (!unixSeconds) return '';
    const diffSec = Math.round(unixSeconds - Date.now() / 1000);
    const abs = Math.abs(diffSec);
    const d = Math.floor(abs / 86400);
    const h = Math.floor((abs % 86400) / 3600);
    const m = Math.floor((abs % 3600) / 60);
    const s = abs % 60;

    let pieces;
    if (d > 0)      pieces = `${d}d ${h}h`;
    else if (h > 0) pieces = `${h}h ${m}m`;
    else if (m > 0) pieces = `${m}m`;
    else            pieces = `${s}s`;

    return diffSec >= 0 ? `in ${pieces}` : `overdue ${pieces}`;
  }

  // ---------- Filter + search logic (QAJ-02) --------------------------

  // Returns true if a single job matches the active filter chip.
  function jobMatchesFilter(job, filter) {
    switch (filter) {
      case 'all':
        return true;
      case 'estimates':
        return (job.job_type === '24h' || job.job_type === '3day')
          && (job.sent === 0 || job.sent === undefined);
      case 'completed':
        return job.job_type === 'review' || job.sent === 1;
      case 'sent':
        return job.sent === 1;
      default:
        return true;
    }
  }

  // Returns true if any searchable field on the job contains the needle.
  function jobMatchesSearch(job, needle) {
    if (!needle) return true;
    const hay = [
      job.name, job.phone, job.vin, job.doc_id, job.ro_id, job.email,
      job.vehicle_desc,
    ]
      .filter(Boolean)
      .join(' ')
      .toLowerCase();
    return hay.includes(needle);
  }

  // Group jobs by estimate_key — preserves server ordering (ASC by send_at)
  // inside each group since jobs.forEach walks the array in order.
  function groupByEstimate(jobs) {
    const map = new Map();
    jobs.forEach((job) => {
      // Fallback to doc_id if the server didn't emit an estimate_key yet
      // (pre-QAJ-01 deployments); groups still render one-per-estimate.
      const key = job.estimate_key || job.doc_id || String(job.id);
      if (!map.has(key)) {
        map.set(key, {
          key,
          // Identity + vehicle — copy from the first row; all rows in a
          // group should match since schedule_job now refreshes these
          // fields on every resave.
          name:         job.name || '',
          phone:        job.phone || '',
          email:        job.email || '',
          vehicle_desc: job.vehicle_desc || '',
          vin:          job.vin || '',
          ro_id:        job.ro_id || '',
          jobs:         [],
        });
      }
      map.get(key).jobs.push(job);
    });
    return Array.from(map.values());
  }

  // ---------- Rendering ------------------------------------------------

  function renderQueue(jobs) {
    queueEl.innerHTML = '';
    queueEl.setAttribute('aria-busy', 'false');

    counters.pending = jobs ? jobs.filter((j) => !j.sent).length : 0;
    updateStats();

    const groups = groupByEstimate(jobs || []);
    const needle = currentSearch.trim().toLowerCase();

    // Build visibility decisions up-front so we can detect "no results".
    let visibleGroups = 0;
    groups.forEach((group, i) => {
      const visibleJobs = group.jobs.filter(
        (job) => jobMatchesFilter(job, currentFilter)
              && jobMatchesSearch(job, needle),
      );
      if (visibleJobs.length === 0) return;
      visibleGroups += 1;
      queueEl.appendChild(buildEstimateCard(group, visibleJobs, i));
    });

    if (visibleGroups === 0) {
      queueEl.appendChild(emptyTpl.content.cloneNode(true));
      // Customise empty-state copy when filters are active so it doesn't
      // falsely claim "All caught up" while the user has a filter on.
      const titleEl = queueEl.querySelector('.empty-title');
      const subEl   = queueEl.querySelector('.empty-sub');
      if (titleEl && (currentFilter !== 'all' || needle)) {
        titleEl.textContent = 'No matches';
        if (subEl) {
          subEl.textContent = needle
            ? `No jobs match "${currentSearch.trim()}".`
            : 'No jobs in this view right now.';
        }
      }
    }
  }

  function buildEstimateCard(group, visibleJobs, index) {
    const frag = estimateTpl.content.cloneNode(true);
    const card = frag.querySelector('.estimate-card');
    card.style.setProperty('--i', String(index));
    card.dataset.estimateKey = group.key;

    frag.querySelector('.estimate-card__name').textContent =
      group.name || 'Unknown customer';
    frag.querySelector('.estimate-phone').textContent = formatPhone(group.phone);
    const emailEl = frag.querySelector('.estimate-email');
    if (group.email) {
      emailEl.textContent = group.email;
    } else {
      emailEl.remove();
    }

    const vehDescEl = frag.querySelector('.estimate-vehicle-desc');
    const vinEl     = frag.querySelector('.estimate-vin');
    const roEl      = frag.querySelector('.estimate-ro');
    vehDescEl.textContent = group.vehicle_desc || '';
    if (group.vin) {
      vinEl.textContent = maskVIN(group.vin);
      vinEl.setAttribute('title', group.vin);
    }
    if (group.ro_id) {
      roEl.textContent = 'RO ' + group.ro_id;
    }
    if (!group.vehicle_desc && !group.vin && !group.ro_id) {
      frag.querySelector('.estimate-card__vehicle').remove();
    }

    const timeline = frag.querySelector('.timeline');
    // Sort visible jobs ASC by send_at so the timeline reads chronologically
    // (earlier at top). Server already returns ASC for pending rows but a
    // belt-and-braces sort keeps the invariant under any future server
    // changes.
    const sortedJobs = visibleJobs.slice().sort((a, b) => {
      const sa = a.send_at || 0;
      const sb = b.send_at || 0;
      return sa - sb;
    });
    sortedJobs.forEach((job) => {
      timeline.appendChild(buildTimelineEntry(job));
    });

    return frag;
  }

  function buildTimelineEntry(job) {
    const frag = entryTpl.content.cloneNode(true);
    const li   = frag.querySelector('.timeline__entry');
    li.dataset.jobId = String(job.id);
    li.dataset.state = job.sent === 1 ? 'sent' : 'pending';

    const chip = frag.querySelector('.job-chip');
    chip.textContent = JOB_TYPE_LABELS[job.job_type] || job.job_type;
    chip.setAttribute('data-type', job.job_type);

    frag.querySelector('.job-send-relative').textContent = formatRelative(job.send_at);
    frag.querySelector('.job-send-absolute').textContent = formatAbsolute(job.send_at);

    // Show a "Sent at …" stamp instead of scheduled-send once delivered.
    if (job.sent === 1) {
      const stampEl = frag.querySelector('.timeline__sent-stamp');
      stampEl.hidden = false;
      const whenTs = job.sent_at && job.sent_at > 0 ? job.sent_at : job.send_at;
      stampEl.textContent = 'Sent · ' + formatAbsolute(whenTs);
      frag.querySelector('.job-send').hidden = true;
    }

    frag.querySelector('.sms-bubble').textContent = previewSMS(job.job_type, job.name);

    const sendBtn   = frag.querySelector('.send-now-btn');
    const cancelBtn = frag.querySelector('.cancel-btn');
    const errEl     = frag.querySelector('.job-error');
    sendBtn.addEventListener('click',   () => handleSendNow(job, li, sendBtn, errEl));
    cancelBtn.addEventListener('click', () => handleCancel(job, li, cancelBtn, errEl));

    return frag;
  }

  function updateStats() {
    statPendingEl.textContent = String(counters.pending);
    statSentEl.textContent    = String(counters.sentToday);
    statFailedEl.textContent  = String(counters.failed);
  }

  // ---------- Send-now flow -------------------------------------------

  async function handleSendNow(job, entryEl, btnEl, errEl) {
    const name = job.name || 'this customer';
    const type = JOB_TYPE_LABELS[job.job_type] || job.job_type;
    const confirmMsg = `Send "${type}" SMS to ${name} right now?`;
    if (!window.confirm(confirmMsg)) return;

    btnEl.disabled = true;
    errEl.hidden = true;
    errEl.textContent = '';

    try {
      // Go admin: POST /api/send-now   Python: POST /earlscheibconcord/queue/send-now
      const sendNowURL = IS_LOCAL_ADMIN
        ? '/api/send-now'
        : `${API_BASE}/queue/send-now`;
      const resp = await fetch(sendNowURL, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ id: job.id }),
      });

      if (resp.status === 200) {
        counters.sentToday++;
        updateStats();
        markEntrySent(entryEl);
        // Re-fetch so the row updates to sent/drops out of pending.
        setTimeout(fetchQueue, 600);
        return;
      }

      if (resp.status === 404) {
        counters.failed++;
        updateStats();
        showEntryError(errEl, 'Already sent or cancelled — refreshing list…');
        setTimeout(fetchQueue, 600);
        return;
      }

      const parsed = await resp.json().catch(() => ({}));
      const msg = parsed.error ? `Send failed: ${parsed.error}` : `Send failed (${resp.status})`;
      counters.failed++;
      updateStats();
      showEntryError(errEl, msg);
      btnEl.disabled = false;

    } catch (e) {
      counters.failed++;
      updateStats();
      showEntryError(errEl, 'Network error — please retry');
      btnEl.disabled = false;
    }
  }

  function markEntrySent(entryEl) {
    const stamp = timeFmt.format(new Date());
    entryEl.dataset.state = 'sent';
    const stampEl = entryEl.querySelector('.timeline__sent-stamp');
    if (stampEl) {
      stampEl.hidden = false;
      stampEl.textContent = 'Sent · ' + stamp;
    }
    const sendMeta = entryEl.querySelector('.job-send');
    if (sendMeta) sendMeta.hidden = true;
  }

  function showEntryError(errEl, msg) {
    errEl.textContent = msg;
    errEl.hidden = false;
    setTimeout(() => { errEl.hidden = true; }, 5000);
  }

  // ---------- Cancel flow ---------------------------------------------

  async function handleCancel(job, entryEl, btnEl, errEl) {
    const name = job.name || 'this customer';
    const type = JOB_TYPE_LABELS[job.job_type] || job.job_type;
    if (!window.confirm(`Cancel the "${type}" follow-up for ${name}?`)) return;

    entryEl.dataset.state = 'cancelling';
    btnEl.disabled = true;
    errEl.hidden = true;

    try {
      // Go admin: POST /api/cancel with {id}
      // Python:   DELETE /earlscheibconcord/queue with {id}
      // Body shape is identical; only method + path differ.
      const resp = await fetch(
        IS_LOCAL_ADMIN ? '/api/cancel' : `${API_BASE}/queue`,
        {
          method: IS_LOCAL_ADMIN ? 'POST' : 'DELETE',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ id: job.id }),
        },
      );
      if (resp.ok) {
        entryEl.style.transition = 'opacity 240ms ease, transform 240ms ease';
        entryEl.style.opacity = '0';
        entryEl.style.transform = 'translateX(-8px)';
        setTimeout(() => {
          const parentCard = entryEl.closest('.estimate-card');
          entryEl.remove();
          // If this was the last visible entry in the estimate card, drop
          // the whole card — main.js re-renders on the next fetchQueue
          // tick anyway, but this keeps the UI tidy in the interim.
          if (parentCard && !parentCard.querySelector('.timeline__entry')) {
            parentCard.remove();
          }
          fetchQueue();
        }, 260);
      } else {
        const parsed = await resp.json().catch(() => ({}));
        const msg = parsed.error ? `Cancel failed: ${parsed.error}` : `Cancel failed (${resp.status})`;
        counters.failed++;
        updateStats();
        entryEl.dataset.state = 'pending';
        btnEl.disabled = false;
        showEntryError(errEl, msg);
      }
    } catch (_) {
      counters.failed++;
      updateStats();
      entryEl.dataset.state = 'pending';
      btnEl.disabled = false;
      showEntryError(errEl, 'Network error — please retry');
    }
  }

  // ---------- Data fetch + refresh ------------------------------------

  async function fetchQueue() {
    if (refreshBtn) refreshBtn.classList.add('spinning');
    try {
      const resp = await fetch(`${API_BASE}/queue`, { cache: 'no-store' });
      if (!resp.ok) {
        const parsed = await resp.json().catch(() => ({}));
        showError(parsed.error || `queue fetch failed (${resp.status})`);
        handlePollFailure();
        return;
      }
      const data = await resp.json();
      // Success — exit retry mode if we were in it.
      onPollSuccess();
      lastJobs = Array.isArray(data) ? data : [];
      renderQueue(lastJobs);
      pulseSyncDot();
      lastFetchedAt = Date.now();
      updateSyncCaption();
    } catch (e) {
      handlePollFailure();
    } finally {
      setTimeout(() => refreshBtn && refreshBtn.classList.remove('spinning'), 500);
    }
  }

  function onPollSuccess() {
    if (inRetryMode) {
      inRetryMode = false;
      retryAttempt = 0;
      if (retryTimerId) { clearTimeout(retryTimerId); retryTimerId = null; }
    }
  }

  function handlePollFailure() {
    retryAttempt++;
    if (retryAttempt >= RETRY_LIMIT) {
      // 60-second window elapsed — show the sleep panel. User must
      // manually wake the admin by relaunching earlscheib.exe --admin.
      showSleepPanel();
      return;
    }
    inRetryMode = true;
    if (retryTimerId) clearTimeout(retryTimerId);
    retryTimerId = setTimeout(fetchQueue, RETRY_MS);
  }

  function showError(msg) {
    queueEl.innerHTML = '';
    const frag = errorTpl.content.cloneNode(true);
    frag.querySelector('.error-msg').textContent = msg;
    queueEl.appendChild(frag);
  }

  function showSleepPanel() {
    queueEl.innerHTML = '';
    queueEl.appendChild(sleepTpl.content.cloneNode(true));
  }

  function pulseSyncDot() {
    if (!syncDotEl) return;
    syncDotEl.classList.remove('sync-dot--pulse');
    // force reflow so the animation restarts cleanly
    void syncDotEl.offsetWidth;
    syncDotEl.classList.add('sync-dot--pulse');
  }

  function updateSyncCaption() {
    if (!lastFetchedAt || !syncCaptionEl) return;
    const ago = Math.max(0, Math.round((Date.now() - lastFetchedAt) / 1000));
    if (ago < 2) {
      syncCaptionEl.textContent = 'Last synced just now';
    } else if (ago < 60) {
      syncCaptionEl.textContent = `Last synced ${ago}s ago`;
    } else {
      const m = Math.floor(ago / 60);
      syncCaptionEl.textContent = `Last synced ${m}m ago`;
    }
  }

  // ---------- Diagnostic panel ----------------------------------------

  async function fetchDiagnostic() {
    try {
      const resp = await fetch(`${API_BASE}/diagnostic`, { cache: 'no-store' });
      if (!resp.ok) return;
      const d = await resp.json();

      if (IS_LOCAL_ADMIN) {
        renderLocalDiagnostic(d);
      } else {
        renderServerDiagnostic(d);
      }
    } catch (_) {
      // Transient errors at 5s poll cadence — silent by design.
    }
  }

  // Go admin (Marco's local binary) returns client-side fields —
  // watch_folder on disk, file count, last scan result, baked-in HMAC secret.
  function renderLocalDiagnostic(d) {
    setDiagText('diag-watch-folder', d.watch_folder || '—');

    const existsTxt = d.folder_exists
      ? 'exists'
      : 'missing' + (d.folder_error ? ' — ' + d.folder_error : '');
    setDiagStatus('diag-folder-exists', d.folder_exists, existsTxt);

    setDiagText('diag-file-count',
      d.folder_exists
        ? `${d.file_count} file${d.file_count === 1 ? '' : 's'}`
        : (d.folder_error || '—')
    );

    setDiagText('diag-last-scan',
      d.last_scan_at
        ? `${d.last_scan_at} — ${d.last_scan_processed} processed, ${d.last_scan_errors} errors`
        : (d.last_scan_note || 'no scans yet')
    );

    setDiagText('diag-last-heartbeat', d.last_heartbeat_at || '—');

    setDiagStatus('diag-hmac', d.hmac_secret_present,
      d.hmac_secret_present ? 'yes' : 'NO — dev build or GSD_HMAC_SECRET unset');

    setDiagText('diag-version', d.app_version || 'dev');
  }

  // Server-side /earlscheibconcord/diagnostic returns a different shape —
  // it can't see Marco's local filesystem, so we surface the operator-useful
  // fields it does know: whether Marco's watcher is currently checking in,
  // how long ago the last heartbeat arrived, active operator commands,
  // and the tail of the most recent uploaded log.
  function renderServerDiagnostic(d) {
    const hb = d.last_heartbeat || {};
    const hbSecs = typeof hb.seconds_ago === 'number' ? hb.seconds_ago : null;
    const clientOnline = !!d.client_online;

    setDiagText('diag-watch-folder', hb.host ? `${hb.host} (shop PC)` : '—');
    setDiagStatus('diag-folder-exists', clientOnline,
      clientOnline ? 'online' : 'offline');
    setDiagText('diag-file-count',
      typeof d.received_logs_count === 'number'
        ? `${d.received_logs_count} log upload${d.received_logs_count === 1 ? '' : 's'}`
        : '—'
    );

    let cmdsText = 'idle';
    if (d.commands_state && typeof d.commands_state === 'object') {
      const active = Object.entries(d.commands_state)
        .filter(([, v]) => v === true || (v && v !== 0 && v !== '0'))
        .map(([k]) => k);
      if (active.length) cmdsText = active.join(', ');
    }
    setDiagText('diag-last-scan', cmdsText);

    if (hbSecs === null) {
      setDiagText('diag-last-heartbeat', hb.ts ? 'recent' : '—');
    } else if (hbSecs < 60) {
      setDiagText('diag-last-heartbeat', `${hbSecs}s ago`);
    } else if (hbSecs < 3600) {
      setDiagText('diag-last-heartbeat', `${Math.floor(hbSecs / 60)}m ago`);
    } else {
      setDiagText('diag-last-heartbeat', `${Math.floor(hbSecs / 3600)}h ago`);
    }

    setDiagStatus('diag-hmac', true, 'server-authed');
    setDiagText('diag-version', 'server');
  }

  function setDiagText(id, txt) {
    const el = document.getElementById(id);
    if (el) el.textContent = txt;
  }

  function relabelDiagnosticForServer() {
    const relabels = {
      'diag-watch-folder':  'Shop PC',
      'diag-folder-exists': 'Client status',
      'diag-file-count':    'Logs received',
      'diag-last-scan':     'Active commands',
      'diag-last-heartbeat':'Last heartbeat',
      'diag-hmac':          'Auth',
      'diag-version':       'Source',
    };
    for (const [id, label] of Object.entries(relabels)) {
      const dd = document.getElementById(id);
      if (!dd) continue;
      const dt = dd.previousElementSibling;
      if (dt && dt.tagName === 'DT') dt.textContent = label;
    }
  }

  function setDiagStatus(id, ok, txt) {
    const el = document.getElementById(id);
    if (!el) return;
    el.textContent = txt;
    el.classList.toggle('ok', !!ok);
    el.classList.toggle('bad', !ok);
  }

  // ---------- Heartbeat ------------------------------------------------

  function sendAlive() {
    // /alive only exists on the local Go admin — it's how the admin detects
    // the operator tab is still open and keeps itself alive. When this page
    // is served from app.py (public /earlscheib), there's no watchdog to
    // ping, so silently skip.
    if (!IS_LOCAL_ADMIN) return;
    try {
      fetch('/alive', { method: 'POST', keepalive: true, body: '' }).catch(() => {});
    } catch (_) { /* never throw from heartbeat */ }
  }

  // ---------- Keyboard shortcut ---------------------------------------

  function isEditingTarget(el) {
    if (!el) return false;
    const tag = (el.tagName || '').toLowerCase();
    return tag === 'input' || tag === 'textarea' || el.isContentEditable;
  }

  // ---------- Filter + search wiring (QAJ-02) -------------------------

  function wireFilters() {
    document.querySelectorAll('.filter').forEach((btn) => {
      btn.addEventListener('click', () => {
        const f = btn.getAttribute('data-filter') || 'all';
        if (f === currentFilter) return;
        currentFilter = f;
        document.querySelectorAll('.filter').forEach((b) => {
          b.setAttribute(
            'aria-selected',
            b.getAttribute('data-filter') === f ? 'true' : 'false',
          );
        });
        renderQueue(lastJobs);
      });
    });
  }

  function wireSearch() {
    if (!searchEl) return;
    searchEl.addEventListener('input', () => {
      const val = searchEl.value;
      if (searchTimerId) clearTimeout(searchTimerId);
      searchTimerId = setTimeout(() => {
        currentSearch = val;
        renderQueue(lastJobs);
      }, SEARCH_DEBOUNCE_MS);
    });
  }

  // ---------- Wire up --------------------------------------------------

  document.addEventListener('DOMContentLoaded', () => {
    wireFilters();
    wireSearch();

    // Public-mode diagnostic labels: the same <dd> elements carry different
    // data when talking to app.py (server-centric) vs the Go admin
    // (client-centric). Rename the <dt> captions so operators don't see
    // "WATCH FOLDER" pointing at a host name.
    if (!IS_LOCAL_ADMIN) relabelDiagnosticForServer();

    fetchQueue();
    setInterval(fetchQueue, REFRESH_MS);
    setInterval(updateSyncCaption, 1000);
    fetchDiagnostic();
    setInterval(fetchDiagnostic, DIAG_MS);
    sendAlive();
    setInterval(sendAlive, HEARTBEAT_MS);

    document.addEventListener('visibilitychange', () => {
      if (!document.hidden) sendAlive();
    });

    if (refreshBtn) {
      refreshBtn.addEventListener('click', fetchQueue);
    }

    document.addEventListener('keydown', (ev) => {
      if (isEditingTarget(ev.target)) return;
      if ((ev.key === 'r' || ev.key === 'R') && !ev.metaKey && !ev.ctrlKey && !ev.altKey) {
        ev.preventDefault();
        fetchQueue();
      }
    });
  });
})();
