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

  // SMS template fallbacks — used by the queue SMS preview bubble when the
  // /templates endpoint hasn't yet been loaded, or as the ultimate fallback
  // on a stale render. Must stay in lock-step with app.py DEFAULT_TEMPLATES.
  // Marco-customised copy flows through GET /templates (WMH-02) and is cached
  // in `effectiveTemplates` below; previewSMS prefers that cache.
  const SMS_TEMPLATES = {
    '24h':
      'Hi {first_name}, this is {shop_name}. Just following up on your recent estimate. ' +
      'Have questions or ready to schedule? Call us at {shop_phone}.',
    '3day':
      'Hi {first_name}, {shop_name} checking in about your estimate from a few days ago. ' +
      "We'd love to help get your car looking great! Call {shop_phone}.",
    'review':
      'Hi {first_name}, thank you for choosing {shop_name}! Hope you\'re happy with your repair. ' +
      'Would you mind leaving us a Google review? It means a lot: {review_url}',
  };
  const SHOP_CONSTANTS = {
    shop_name:  'Earl Scheib Auto Body Concord',
    shop_phone: '(925) 609-7780',
    review_url: 'https://g.page/r/review',
  };

  // Renders a template body against a per-row context (job fields + shop
  // constants). Missing placeholders render as empty string, matching the
  // server's defaultdict-based render_template.
  function renderTemplate(tpl, ctx) {
    if (!tpl) return '';
    const bag = Object.assign({}, SHOP_CONSTANTS, ctx || {});
    if (!bag.first_name && bag.name) {
      bag.first_name = String(bag.name).split(/\s+/)[0];
    }
    if (!bag.first_name) bag.first_name = 'there';
    return tpl.replace(/\{(\w+)\}/g, (_, key) => {
      const v = bag[key];
      return (v === undefined || v === null) ? '' : String(v);
    });
  }

  // WMH-04: cache of effective bodies per job_type — hydrated from
  // GET /templates on first Templates-tab visit and refreshed after every
  // Save/Reset. Used by the queue-page SMS preview bubble so the preview
  // matches Marco's edits without a second round-trip.
  const effectiveTemplates = {};

  function previewSMS(jobType, job) {
    // Legacy call-sites pass a bare name string. Normalise to an object.
    const ctx = (typeof job === 'string')
      ? { name: job }
      : (job || {});
    const tpl = effectiveTemplates[jobType] || SMS_TEMPLATES[jobType] || '';
    return renderTemplate(tpl, ctx);
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

    frag.querySelector('.sms-bubble').textContent = previewSMS(job.job_type, job);

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

  // ---------- Templates page (WMH-04) ---------------------------------
  //
  // URL paths (respect API_BASE_PATH):
  //   Go admin  : GET /api/templates            PUT /api/templates/{jt}
  //   Public UI : GET /earlscheibconcord/templates PUT /earlscheibconcord/templates/{jt}
  //
  // The tab is loaded lazily on first click; subsequent clicks reuse the
  // already-mounted DOM. Refresh happens after each Save/Reset.

  const tplListURL   = IS_LOCAL_ADMIN
    ? '/api/templates'
    : `${API_BASE}/templates`;
  const tplUpsertURL = (jt) => IS_LOCAL_ADMIN
    ? `/api/templates/${encodeURIComponent(jt)}`
    : `${API_BASE}/templates/${encodeURIComponent(jt)}`;

  const templateState = {
    // Map of job_type -> { body: <saved body>, is_override: bool, label, when }
    cards:         {},
    sample:        {},
    placeholders:  { per_row: [], shop: [] },
    loaded:        false,
  };

  function getTemplatesListEl() {
    return document.getElementById('templates-list');
  }

  async function loadTemplates(force) {
    const listEl = getTemplatesListEl();
    if (!listEl) return;
    if (templateState.loaded && !force) return;

    listEl.innerHTML = '';
    const loading = document.createElement('p');
    loading.className = 'templates-loading';
    loading.textContent = 'Loading templates…';
    listEl.appendChild(loading);

    try {
      const resp = await fetch(tplListURL, { cache: 'no-store' });
      if (!resp.ok) {
        listEl.innerHTML = '';
        const err = document.createElement('p');
        err.className = 'templates-error';
        err.textContent = `Couldn't load templates (${resp.status}).`;
        listEl.appendChild(err);
        return;
      }
      const data = await resp.json();
      templateState.sample       = data.sample_row || {};
      templateState.placeholders = data.placeholders || { per_row: [], shop: [] };
      templateState.cards        = {};
      listEl.innerHTML = '';
      (data.job_types || []).forEach((jt) => {
        templateState.cards[jt.job_type] = {
          body:        jt.body,
          is_override: !!jt.is_override,
          label:       jt.label,
          when:        jt.when,
        };
        // Refresh queue-page preview cache with the effective body.
        effectiveTemplates[jt.job_type] = jt.body;
        listEl.appendChild(buildTemplateCard(jt));
      });
      templateState.loaded = true;
    } catch (_) {
      listEl.innerHTML = '';
      const err = document.createElement('p');
      err.className = 'templates-error';
      err.textContent = "Couldn't reach the server. Please retry.";
      listEl.appendChild(err);
    }
  }

  function buildTemplateCard(jt) {
    const tpl = document.getElementById('template-card-template');
    const frag = tpl.content.cloneNode(true);
    const article = frag.querySelector('.tpl-card');
    article.dataset.jobType = jt.job_type;

    frag.querySelector('.tpl-card__title').textContent = jt.label;
    frag.querySelector('.tpl-card__when').textContent  = jt.when;

    const badge = frag.querySelector('.tpl-card__badge');
    badge.hidden = !jt.is_override;

    // Variable chips — per-row first, then shop constants.
    const chipsEl = frag.querySelector('.tpl-card__chips');
    const allPlaceholders = (templateState.placeholders.per_row || [])
      .concat(templateState.placeholders.shop || []);
    allPlaceholders.forEach((name) => {
      const chip = document.createElement('button');
      chip.type = 'button';
      chip.className = 'tpl-chip';
      chip.textContent = `{${name}}`;
      chip.setAttribute('data-var', name);
      chipsEl.appendChild(chip);
    });

    // Textarea + preview + counter.
    const taId = `tpl-body-${jt.job_type}`;
    const textarea = frag.querySelector('.tpl-card__body');
    const label    = frag.querySelector('.tpl-card__label');
    textarea.id    = taId;
    textarea.value = jt.body || '';
    label.setAttribute('for', taId);
    label.textContent = 'Message body';

    const counter   = frag.querySelector('.tpl-card__count');
    const previewEl = frag.querySelector('.tpl-card__preview-body');
    const saveBtn   = frag.querySelector('.tpl-save');
    const resetBtn  = frag.querySelector('.tpl-reset');
    const dirtyDot  = frag.querySelector('.tpl-card__dirty-dot');
    const statusEl  = frag.querySelector('.tpl-card__status');

    // Initial render.
    counter.textContent = String(textarea.value.length);
    previewEl.textContent = renderTemplate(textarea.value, templateState.sample);
    resetBtn.disabled = !jt.is_override;

    // Chip click -> insert at cursor.
    chipsEl.addEventListener('click', (ev) => {
      const btn = ev.target.closest('.tpl-chip');
      if (!btn) return;
      ev.preventDefault();
      const v = btn.getAttribute('data-var');
      insertAtCursor(textarea, `{${v}}`);
      // Trigger input handler for preview + dirty-state refresh.
      textarea.dispatchEvent(new Event('input', { bubbles: true }));
    });

    // Debounced preview + dirty tracking.
    let previewTimer = null;
    textarea.addEventListener('input', () => {
      counter.textContent = String(textarea.value.length);
      if (previewTimer) clearTimeout(previewTimer);
      previewTimer = setTimeout(() => {
        previewEl.textContent = renderTemplate(textarea.value, templateState.sample);
      }, SEARCH_DEBOUNCE_MS);

      const saved = templateState.cards[jt.job_type] || { body: '' };
      const isDirty = textarea.value !== saved.body;
      dirtyDot.hidden = !isDirty;
      saveBtn.disabled = !isDirty;
      // Reset enabled when an override exists (regardless of dirty) so Marco
      // can revert to default after saving without needing to clear the box.
      resetBtn.disabled = !saved.is_override;
      // Clear inline error state once the user edits again.
      statusEl.textContent = '';
      statusEl.removeAttribute('data-state');
    });

    // Save.
    saveBtn.addEventListener('click', async () => {
      const val = textarea.value;
      // Instant client-side validation: balanced braces. Server validates
      // authoritatively; this just gives instant UX feedback.
      if (!bracesBalanced(val)) {
        showStatus(statusEl, 'Unclosed { or mismatched braces', 'error');
        return;
      }
      saveBtn.disabled = true;
      resetBtn.disabled = true;
      try {
        const resp = await fetch(tplUpsertURL(jt.job_type), {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ body: val }),
        });
        const parsed = await resp.json().catch(() => ({}));
        if (!resp.ok) {
          const msg = parsed.detail || parsed.error || `Save failed (${resp.status})`;
          showStatus(statusEl, msg, 'error');
          // Re-enable so user can retry.
          saveBtn.disabled = false;
          resetBtn.disabled = !(templateState.cards[jt.job_type] || {}).is_override;
          return;
        }
        applySavedTemplate(jt.job_type, parsed, textarea, badge, dirtyDot,
                           saveBtn, resetBtn, statusEl, previewEl);
      } catch (_) {
        showStatus(statusEl, 'Network error — please retry', 'error');
        saveBtn.disabled = false;
        resetBtn.disabled = !(templateState.cards[jt.job_type] || {}).is_override;
      }
    });

    // Reset to default — PUT with empty body (server DELETEs the row).
    resetBtn.addEventListener('click', async () => {
      if (!window.confirm('Restore the default copy for this follow-up?')) return;
      saveBtn.disabled = true;
      resetBtn.disabled = true;
      try {
        const resp = await fetch(tplUpsertURL(jt.job_type), {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ body: '' }),
        });
        const parsed = await resp.json().catch(() => ({}));
        if (!resp.ok) {
          showStatus(statusEl, parsed.error || `Reset failed (${resp.status})`, 'error');
          saveBtn.disabled = false;
          resetBtn.disabled = false;
          return;
        }
        applySavedTemplate(jt.job_type, parsed, textarea, badge, dirtyDot,
                           saveBtn, resetBtn, statusEl, previewEl);
      } catch (_) {
        showStatus(statusEl, 'Network error — please retry', 'error');
        saveBtn.disabled = false;
        resetBtn.disabled = false;
      }
    });

    return frag;
  }

  function applySavedTemplate(jobType, parsed, textarea, badge, dirtyDot,
                              saveBtn, resetBtn, statusEl, previewEl) {
    const newBody = typeof parsed.body === 'string' ? parsed.body : '';
    textarea.value = newBody;
    const tCount = textarea.parentElement
      ? textarea.parentElement.querySelector('.tpl-card__count')
      : null;
    if (tCount) tCount.textContent = String(newBody.length);
    previewEl.textContent = renderTemplate(newBody, templateState.sample);

    templateState.cards[jobType] = {
      body:        newBody,
      is_override: !!parsed.is_override,
      label:       (templateState.cards[jobType] || {}).label,
      when:        (templateState.cards[jobType] || {}).when,
    };
    effectiveTemplates[jobType] = newBody;

    badge.hidden = !parsed.is_override;
    dirtyDot.hidden = true;
    saveBtn.disabled = true;
    resetBtn.disabled = !parsed.is_override;

    const msg = parsed.is_override ? 'Saved · just now' : 'Reverted to default';
    showStatus(statusEl, msg, 'ok');
  }

  function showStatus(el, msg, kind) {
    el.textContent = msg;
    if (kind) el.setAttribute('data-state', kind);
    else el.removeAttribute('data-state');
    if (kind === 'ok') {
      setTimeout(() => {
        // Only clear if still showing the same message.
        if (el.textContent === msg) {
          el.textContent = '';
          el.removeAttribute('data-state');
        }
      }, 3000);
    }
  }

  function insertAtCursor(textarea, snippet) {
    const start = textarea.selectionStart;
    const end   = textarea.selectionEnd;
    const before = textarea.value.slice(0, start);
    const after  = textarea.value.slice(end);
    textarea.value = before + snippet + after;
    const caret = start + snippet.length;
    textarea.focus();
    textarea.setSelectionRange(caret, caret);
  }

  // Cheap brace-balance check — counts { and }, rejects when they don't match
  // or when a } appears before any {. Matches str.format_map's coarse
  // validator closely enough for instant UX feedback.
  function bracesBalanced(s) {
    let depth = 0;
    for (let i = 0; i < s.length; i += 1) {
      const c = s.charAt(i);
      if (c === '{') depth += 1;
      else if (c === '}') {
        depth -= 1;
        if (depth < 0) return false;
      }
    }
    return depth === 0;
  }

  function wireTopnav() {
    const links = document.querySelectorAll('.topnav-link');
    const viewQueue = document.getElementById('view-queue');
    const viewTpl   = document.getElementById('view-templates');
    if (!links.length || !viewQueue || !viewTpl) return;

    const activate = (target) => {
      links.forEach((a) => {
        const on = a.getAttribute('data-view') === target;
        a.classList.toggle('is-active', on);
        a.setAttribute('aria-selected', on ? 'true' : 'false');
      });
      viewQueue.hidden = target !== 'queue';
      viewTpl.hidden   = target !== 'templates';
      if (target === 'templates') loadTemplates(false);
    };

    links.forEach((a) => {
      a.addEventListener('click', (ev) => {
        ev.preventDefault();
        const target = a.getAttribute('data-view') || 'queue';
        activate(target);
      });
    });

    // Allow direct-link via hash (e.g. refreshes on /earlscheib#templates).
    if (window.location.hash === '#templates') activate('templates');
  }

  // ---------- Wire up --------------------------------------------------

  document.addEventListener('DOMContentLoaded', () => {
    wireFilters();
    wireSearch();
    wireTopnav();

    // Public-mode diagnostic labels: the same <dd> elements carry different
    // data when talking to app.py (server-centric) vs the Go admin
    // (client-centric). Rename the <dt> captions so operators don't see
    // "WATCH FOLDER" pointing at a host name.
    if (!IS_LOCAL_ADMIN) relabelDiagnosticForServer();

    // WMH-04: prime the effective-templates cache once so the queue-page
    // SMS preview reflects Marco's saved copy even if he never visits the
    // Templates tab this session. Fires and forgets — failures are silent
    // because the queue bubbles fall back to SMS_TEMPLATES[*] defaults.
    fetch(tplListURL, { cache: 'no-store' })
      .then((r) => (r && r.ok ? r.json() : null))
      .then((data) => {
        if (!data || !Array.isArray(data.job_types)) return;
        data.job_types.forEach((jt) => { effectiveTemplates[jt.job_type] = jt.body; });
        // If any queue cards are already mounted, refresh their preview bubbles.
        document.querySelectorAll('.timeline__entry').forEach((li) => {
          // jobId is set when an entry is built; its parent card has the job
          // object encoded in its chip + bubble. Simplest refresh: re-run
          // fetchQueue so the whole thing rebuilds against lastJobs.
        });
        if (lastJobs && lastJobs.length) renderQueue(lastJobs);
      })
      .catch(() => { /* silent — defaults handle it */ });

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
