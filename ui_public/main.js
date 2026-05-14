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
      'Hi {first_name}, this is {shop_name}. Just following up on the estimate ' +
      'for your {year} {make}. Have questions or ready to schedule? ' +
      'Call us at {shop_phone}.',
    '3day':
      'Hi {first_name}, {shop_name} checking in about the estimate for your ' +
      "{year} {make} from a few days ago. We'd love to help get it looking " +
      'great! Call {shop_phone}.',
    'review':
      'Hi {first_name}, thank you for choosing {shop_name}! Hope you\'re happy ' +
      'with the repair on your {year} {make}. Would you mind leaving us a ' +
      'Google review? It means a lot: {review_url}',
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
    if (!bag.short_model && bag.model) {
      bag.short_model = String(bag.model).split(/\s+/).slice(0, 2).join(' ');
    }
    // Expand 2-digit year → 4-digit ("22" → "2022", "96" → "1996").
    if (bag.year) {
      const yr = String(bag.year).trim();
      if (/^\d{2}$/.test(yr)) {
        bag.year = (parseInt(yr, 10) <= 30 ? '20' : '19') + yr;
      }
    }
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

  // SPN-05: format hours → "(X day[s])" helper text, e.g. 24 → "(1 day)",
  // 36 → "(1.5 days)", 72 → "(3 days)". One decimal max so the label stays
  // readable. Returns "" for non-positive / non-numeric input.
  function hoursToDaysLabel(h) {
    const n = Number(h);
    if (!Number.isFinite(n) || n <= 0) return '';
    const days = Math.round((n / 24) * 10) / 10;
    const word = days === 1 ? 'day' : 'days';
    return `(${days} ${word})`;
  }

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
  let currentFilter = 'estimates';
  let currentSearch = '';
  let lastJobs      = [];
  let searchTimerId = null;

  // Sort selector state. Persisted in localStorage so reload keeps the user's
  // choice. Default "newest" matches the server-side ORDER BY DESC already in
  // place. Valid values: "newest" | "oldest" | "next-send" | "customer".
  let currentSort = (function () {
    try {
      const stored = localStorage.getItem('eswSort');
      if (stored && /^(newest|oldest|next-send|customer)$/.test(stored)) return stored;
    } catch (_) { /* localStorage unavailable — fall through to default */ }
    return 'newest';
  })();

  // Anti-jitter: skip a full DOM rebuild when neither the data nor the
  // filter/search has changed since the last render. Without this the 15s
  // poll wipes innerHTML and re-runs the staggered fadeUp animation, which
  // reads as visible flicker even when nothing changed.
  let lastRenderKey = '';

  function jobsRenderKey(jobs, filter, search, sort) {
    const parts = (jobs || []).map((j) =>
      `${j.id}:${j.sent || 0}:${j.send_at || 0}:${j.sent_at || 0}`,
    );
    return `${filter}|${search}|${sort}|${parts.join(',')}`;
  }

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
  //
  // GLV-01: every named "what's pending" pill (estimates, completed, and the
  // test-* variants) excludes already-sent rows. Only the dedicated `sent`
  // pill (and `all`) surface sent rows — that is where the Resend button is
  // intended to live.
  //
  // GLV-02: cancelled rows (cancelled=1) appear ONLY under the dedicated
  // `cancelled` pill and under `all`. Never under pending, sent, estimates,
  // completed, or test-* views. They never get a Resend button.
  function jobMatchesFilter(job, filter) {
    const isTest = job.is_test === 1;
    const isCancelled = job.cancelled === 1;
    const isPending = !isCancelled
      && (job.sent === 0 || job.sent === undefined);
    switch (filter) {
      case 'all':
        return !isTest;
      case 'estimates':
        return !isTest && (job.job_type === '24h' || job.job_type === '3day')
          && isPending;
      case 'completed':
        return !isTest && job.job_type === 'review' && isPending;
      case 'sent':
        return !isTest && job.sent === 1 && !isCancelled;
      case 'cancelled':
        return !isTest && isCancelled;
      case 'test-estimates':
        return isTest && (job.job_type === '24h' || job.job_type === '3day')
          && isPending;
      case 'test-completed':
        return isTest && job.job_type === 'review' && isPending;
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
          created_at:   job.created_at || 0,
          jobs:         [],
        });
      }
      map.get(key).jobs.push(job);
    });
    return Array.from(map.values());
  }

  // Sort estimate-card groups according to the user's "Sort" dropdown.
  // - newest    : group's most recent activity timestamp, DESC (default)
  // - oldest    : same, ASC
  // - next-send : earliest pending send_at first; groups with no pending sends
  //               fall to the bottom (treated as +Infinity for the comparator)
  // - customer  : alphabetical by name, A→Z
  // Unknown sort key falls back to "newest" to mirror the server's ORDER BY.
  function sortGroups(groups, sortKey) {
    const newestTs = (g) => g.jobs.reduce(
      (mx, j) => Math.max(mx, j.sent_at || 0, j.send_at || 0, j.created_at || 0),
      0,
    );
    const nextPendingTs = (g) => {
      let m = Infinity;
      g.jobs.forEach((j) => {
        if (!j.sent && j.send_at && j.send_at < m) m = j.send_at;
      });
      return m;
    };
    const cmps = {
      'newest':    (a, b) => newestTs(b) - newestTs(a),
      'oldest':    (a, b) => newestTs(a) - newestTs(b),
      'next-send': (a, b) => nextPendingTs(a) - nextPendingTs(b),
      'customer':  (a, b) => (a.name || '').toLowerCase()
                              .localeCompare((b.name || '').toLowerCase()),
    };
    return groups.slice().sort(cmps[sortKey] || cmps['newest']);
  }

  // ---------- Rendering ------------------------------------------------

  // GLV-03 (260513): "Sent today" was session-local — only incremented on
  // UI clicks in the current tab, never recomputed from server data. So a
  // page refresh, a fresh tab, or a Cloudflare Access re-auth wiped the
  // count back to zero, making real sends invisible to the operator.
  // Compute it from the queue's `sent_at` field using a Pacific-day
  // boundary (matches the timezone the scheduler + send-now logic use).
  // `pending` and `cancelled` are excluded — only rows that genuinely
  // hit Twilio today are counted.
  function recomputeSentToday(jobs) {
    if (!jobs || !jobs.length) return 0;
    const todayPT = new Intl.DateTimeFormat('en-CA', {
      timeZone: 'America/Los_Angeles',
      year: 'numeric', month: '2-digit', day: '2-digit',
    }).format(new Date());
    let n = 0;
    for (const j of jobs) {
      if (j.sent !== 1) continue;
      if (j.cancelled === 1) continue;
      if (!j.sent_at) continue;
      const sentPT = new Intl.DateTimeFormat('en-CA', {
        timeZone: 'America/Los_Angeles',
        year: 'numeric', month: '2-digit', day: '2-digit',
      }).format(new Date(j.sent_at * 1000));
      if (sentPT === todayPT) n += 1;
    }
    return n;
  }

  function renderQueue(jobs) {
    const needle = currentSearch.trim().toLowerCase();
    const renderKey = jobsRenderKey(jobs, currentFilter, needle, currentSort);
    if (renderKey === lastRenderKey) {
      // Data + view unchanged — skip the rebuild so animations don't replay.
      counters.pending   = jobs ? jobs.filter((j) => !j.sent).length : 0;
      counters.sentToday = recomputeSentToday(jobs);
      updateStats();
      return;
    }
    lastRenderKey = renderKey;

    queueEl.innerHTML = '';
    queueEl.setAttribute('aria-busy', 'false');

    counters.pending   = jobs ? jobs.filter((j) => !j.sent).length : 0;
    counters.sentToday = recomputeSentToday(jobs);
    updateStats();

    const groups = sortGroups(groupByEstimate(jobs || []), currentSort);

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

    const dateEl = frag.querySelector('.estimate-date');
    if (dateEl && group.created_at) {
      const d = new Date(group.created_at * 1000);
      dateEl.textContent = d.toLocaleDateString('en-US', { month: 'short', day: 'numeric', year: 'numeric' });
    }

    const timeline = frag.querySelector('.timeline');
    // Sort DESC by most-recent activity so freshly sent / soonest-scheduled
    // entries surface at the top of each estimate card. Uses sent_at when
    // available (sent rows), otherwise send_at — matches the server's
    // COALESCE(sent_at, send_at, created_at) ordering for the "all" view.
    const sortedJobs = visibleJobs.slice().sort((a, b) => {
      const ta = a.sent_at || a.send_at || 0;
      const tb = b.sent_at || b.send_at || 0;
      return tb - ta;
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
    // GLV-02: cancelled rows get their own data-state so CSS can render
    // them distinctly (no Send-now, no Cancel, no Resend — read-only).
    li.dataset.state = job.cancelled === 1
      ? 'cancelled'
      : (job.sent === 1 ? 'sent' : 'pending');

    const chip = frag.querySelector('.job-chip');
    chip.textContent = JOB_TYPE_LABELS[job.job_type] || job.job_type;
    chip.setAttribute('data-type', job.job_type);

    frag.querySelector('.job-send-relative').textContent = formatRelative(job.send_at);
    frag.querySelector('.job-send-absolute').textContent = formatAbsolute(job.send_at);

    // Show a "Sent at …" stamp instead of scheduled-send once delivered.
    // GLV-02: cancelled rows show "Cancelled" instead of "Sent" so the
    // operator can distinguish removed-from-pipeline rows from real sends.
    if (job.cancelled === 1) {
      const stampEl = frag.querySelector('.timeline__sent-stamp');
      stampEl.hidden = false;
      stampEl.textContent = 'Cancelled';
      frag.querySelector('.job-send').hidden = true;
    } else if (job.sent === 1) {
      const stampEl = frag.querySelector('.timeline__sent-stamp');
      stampEl.hidden = false;
      const whenTs = job.sent_at && job.sent_at > 0 ? job.sent_at : job.send_at;
      stampEl.textContent = 'Sent · ' + formatAbsolute(whenTs);
      frag.querySelector('.job-send').hidden = true;
    }

    frag.querySelector('.sms-bubble').textContent = previewSMS(job.job_type, job);

    const sendBtn     = frag.querySelector('.send-now-btn');
    const cancelBtn   = frag.querySelector('.cancel-btn');
    const resendBtn   = frag.querySelector('.resend-btn');
    const uncancelBtn = frag.querySelector('.uncancel-btn');
    const errEl       = frag.querySelector('.job-error');

    // GLV-01 + GLV-02 + GLV-04: pick the right action button per state.
    //   pending   → Send-now + Cancel
    //   sent      → Resend
    //   cancelled → Uncancel
    if (job.cancelled === 1) {
      if (sendBtn)     sendBtn.hidden     = true;
      if (cancelBtn)   cancelBtn.hidden   = true;
      if (resendBtn)   resendBtn.hidden   = true;
      if (uncancelBtn) uncancelBtn.hidden = false;
    } else if (job.sent === 1) {
      if (sendBtn)     sendBtn.hidden     = true;
      if (cancelBtn)   cancelBtn.hidden   = true;
      if (resendBtn)   resendBtn.hidden   = false;
      if (uncancelBtn) uncancelBtn.hidden = true;
    } else {
      if (resendBtn)   resendBtn.hidden   = true;
      if (uncancelBtn) uncancelBtn.hidden = true;
    }

    if (sendBtn)     sendBtn.addEventListener('click',     () => handleSendNow(job, li, sendBtn, errEl));
    if (cancelBtn)   cancelBtn.addEventListener('click',   () => handleCancel(job, li, cancelBtn, errEl));
    if (resendBtn)   resendBtn.addEventListener('click',   () => handleResend(job, li, resendBtn, errEl));
    if (uncancelBtn) uncancelBtn.addEventListener('click', () => handleUncancel(job, li, uncancelBtn, errEl));

    return frag;
  }

  function updateStats() {
    statPendingEl.textContent = String(counters.pending);
    statSentEl.textContent    = String(counters.sentToday);
    statFailedEl.textContent  = String(counters.failed);
  }

  // ---------- Send-now flow -------------------------------------------

  async function handleSendNow(job, entryEl, btnEl, errEl) {
    const type = JOB_TYPE_LABELS[job.job_type] || job.job_type;
    const confirmMsg = job.is_test === 1
      ? `Send "${type}" SMS to test recipients (+15308450190 Jaskarn, +19254215772 Marco) right now?`
      : `Send "${type}" SMS to ${job.name || 'this customer'} right now?`;
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

  // ---------- Resend flow (GLV-01) ------------------------------------

  // Resend an already-sent job. Does NOT flip the row's sent flag — the row
  // stays "sent" and the audit trail of repeated sends lives in sms_log.
  async function handleResend(job, entryEl, btnEl, errEl) {
    const type = JOB_TYPE_LABELS[job.job_type] || job.job_type;
    const confirmMsg = job.is_test === 1
      ? `Resend "${type}" SMS to test recipients now? (original row stays marked sent)`
      : `Resend "${type}" SMS to ${job.name || 'this customer'} now? (original row stays marked sent)`;
    if (!window.confirm(confirmMsg)) return;

    btnEl.disabled = true;
    errEl.hidden = true;
    errEl.textContent = '';
    const origLabel = btnEl.querySelector('span') ? btnEl.querySelector('span').textContent : '';
    if (btnEl.querySelector('span')) btnEl.querySelector('span').textContent = 'Resending…';

    try {
      const resendURL = IS_LOCAL_ADMIN
        ? '/api/resend'
        : `${API_BASE}/queue/resend`;
      const resp = await fetch(resendURL, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ id: job.id }),
      });
      if (resp.status === 200) {
        if (btnEl.querySelector('span')) btnEl.querySelector('span').textContent = 'Resent ✓';
        setTimeout(() => {
          btnEl.disabled = false;
          if (btnEl.querySelector('span')) btnEl.querySelector('span').textContent = origLabel || 'Resend';
        }, 2200);
        return;
      }
      const parsed = await resp.json().catch(() => ({}));
      const msg = parsed.error ? `Resend failed: ${parsed.error}` : `Resend failed (${resp.status})`;
      showEntryError(errEl, msg);
      btnEl.disabled = false;
      if (btnEl.querySelector('span')) btnEl.querySelector('span').textContent = origLabel || 'Resend';
    } catch (e) {
      showEntryError(errEl, 'Network error — please retry');
      btnEl.disabled = false;
      if (btnEl.querySelector('span')) btnEl.querySelector('span').textContent = origLabel || 'Resend';
    }
  }

  // ---------- Uncancel flow (GLV-04) ----------------------------------

  // Flip cancelled=1 back to cancelled=0 on a row so it reappears in
  // Estimates / Work Completed as pending. No SMS is sent — Marco can then
  // click Send-now (or wait for the scheduler when it's re-enabled).
  async function handleUncancel(job, entryEl, btnEl, errEl) {
    const type = JOB_TYPE_LABELS[job.job_type] || job.job_type;
    if (!window.confirm(`Restore "${type}" follow-up for ${job.name || 'this customer'} to pending?`)) return;

    btnEl.disabled = true;
    errEl.hidden = true;
    errEl.textContent = '';

    try {
      const uncancelURL = IS_LOCAL_ADMIN
        ? '/api/uncancel'
        : `${API_BASE}/queue/uncancel`;
      const resp = await fetch(uncancelURL, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ id: job.id }),
      });
      if (resp.status === 200) {
        // Reload the queue so the row drops out of Cancelled and reappears
        // under its native pending filter.
        setTimeout(fetchQueue, 400);
        return;
      }
      const parsed = await resp.json().catch(() => ({}));
      const msg = parsed.error
        ? `Uncancel failed: ${parsed.error}`
        : `Uncancel failed (${resp.status})`;
      showEntryError(errEl, msg);
      btnEl.disabled = false;
    } catch (e) {
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
      const resp = await fetch(`${API_BASE}/queue?status=all`, { cache: 'no-store' });
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

      // SPN-04: dev-mode banner. Show only when scheduler_enabled === false.
      // Hide on undefined so we don't flash the banner during the brief
      // moment before the first /diagnostic poll resolves on a UI version
      // that pre-dates SPN.
      const banner = document.getElementById('dev-banner');
      if (banner) {
        banner.hidden = d.scheduler_enabled !== false;
      }
    } catch (_) {
      // Transient errors at 5s poll cadence — silent by design.
    }
  }

  // Go admin (Marco's local binary) returns client-side fields —
  // watch_folder on disk, file count, last scan result, baked-in HMAC secret.
  // Twilio sender doesn't apply to the local watcher (the Pi is the sender),
  // so we hide the row in local mode rather than leave a permanent "—".
  function renderLocalDiagnostic(d) {
    hideDiagRow('diag-twilio-from');
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

  // Server-side /earlscheibconcord/diagnostic returns the heartbeat from the
  // scanner (Pi-only architecture: the scanner runs locally on the same Pi
  // as the webhook). We surface 3 useful signals: which host most recently
  // checked in, whether it's online (last heartbeat within freshness window),
  // and how long ago the heartbeat arrived. The other fields the upstream
  // payload carries (received_logs_count, commands_state) were meaningful
  // when Marco's Windows PC was a remote client; in the Pi-only setup they
  // never advance past zero/idle, so we hide their rows in
  // relabelDiagnosticForServer() rather than render misleading data.
  function renderServerDiagnostic(d) {
    const hb = d.last_heartbeat || {};
    const hbSecs = typeof hb.seconds_ago === 'number' ? hb.seconds_ago : null;
    const clientOnline = !!d.client_online;

    setDiagText('diag-watch-folder', hb.host || '—');
    setDiagStatus('diag-folder-exists', clientOnline,
      clientOnline ? 'online' : 'offline');

    if (hbSecs === null) {
      setDiagText('diag-last-heartbeat', hb.ts ? 'recent' : '—');
    } else if (hbSecs < 60) {
      setDiagText('diag-last-heartbeat', `${hbSecs}s ago`);
    } else if (hbSecs < 3600) {
      setDiagText('diag-last-heartbeat', `${Math.floor(hbSecs / 60)}m ago`);
    } else {
      setDiagText('diag-last-heartbeat', `${Math.floor(hbSecs / 3600)}h ago`);
    }

    // GLV-incident-260514: masked Twilio sender. Operator-visible so a
    // wrong-number drift (e.g. stale sandbox value in .env) is caught
    // at a glance. Server returns "" when TWILIO_FROM is unset.
    const tf = d.twilio_from_masked;
    setDiagText('diag-twilio-from', tf ? tf : 'NOT SET');
  }

  function setDiagText(id, txt) {
    const el = document.getElementById(id);
    if (el) el.textContent = txt;
  }

  function relabelDiagnosticForServer() {
    // Pi-only architecture (post-OC migration): the watcher is the same host
    // as the webhook, so client-tracking fields no longer carry meaning.
    // Relabel the 3 fields we still populate; hide the 4 fields that would
    // otherwise show "0 log uploads / idle / server-authed / server" forever.
    const relabels = {
      'diag-watch-folder':  'Watcher host',
      'diag-folder-exists': 'Status',
      'diag-last-heartbeat':'Last heartbeat',
    };
    for (const [id, label] of Object.entries(relabels)) {
      const dd = document.getElementById(id);
      if (!dd) continue;
      const dt = dd.previousElementSibling;
      if (dt && dt.tagName === 'DT') dt.textContent = label;
    }
    // Hide rows we no longer populate. Both <dt> and <dd> are toggled so
    // the dl grid collapses cleanly without leaving a blank cell.
    const hideIds = ['diag-file-count', 'diag-last-scan', 'diag-hmac', 'diag-version'];
    hideIds.forEach(hideDiagRow);
  }

  // Shared helper — hides both the <dt> and <dd> of a diagnostic row so the
  // dl grid collapses cleanly.
  function hideDiagRow(id) {
    const dd = document.getElementById(id);
    if (!dd) return;
    const dt = dd.previousElementSibling;
    dd.style.display = 'none';
    if (dt && dt.tagName === 'DT') dt.style.display = 'none';
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

    // Sort dropdown — restore persisted choice on mount, then write through to
    // localStorage on every change. Forces a re-render by clearing the
    // jitter-guard so groups reorder immediately.
    const sortSel = document.getElementById('sort-by');
    if (sortSel) {
      sortSel.value = currentSort;
      sortSel.addEventListener('change', () => {
        const v = sortSel.value;
        if (v === currentSort) return;
        currentSort = v;
        try { localStorage.setItem('eswSort', v); } catch (_) { /* ignore */ }
        renderQueue(lastJobs);
      });
    }

    const resetBtn = document.querySelector('.test-reset-btn');
    if (resetBtn) {
      resetBtn.addEventListener('click', async () => {
        resetBtn.disabled = true;
        resetBtn.textContent = '…';
        try {
          const res = await fetch(`${window.API_BASE_PATH || ''}/reset-test-jobs`, { method: 'POST' });
          if (res.ok) {
            resetBtn.textContent = '✓';
            setTimeout(() => { resetBtn.textContent = '↺'; resetBtn.disabled = false; }, 1200);
            lastJobs = [];
            await fetchQueue();
          } else {
            resetBtn.textContent = '!';
            setTimeout(() => { resetBtn.textContent = '↺'; resetBtn.disabled = false; }, 2000);
          }
        } catch {
          resetBtn.textContent = '!';
          setTimeout(() => { resetBtn.textContent = '↺'; resetBtn.disabled = false; }, 2000);
        }
      });
    }
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

    // GLV-05 (260513): force the queue to re-render the SMS-preview bubbles
    // against the new template body. Without this, the queue's
    // "Customer receives" panel keeps showing the OLD copy until the next
    // job-state change — because `lastRenderKey` only keys on job IDs +
    // sent/cancelled state, not on template content. Clearing the cache
    // key + calling renderQueue rebuilds every visible bubble immediately.
    lastRenderKey = '';
    if (lastJobs && lastJobs.length) renderQueue(lastJobs);

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

  // ---------- Schedules page (SPN-05) ---------------------------------
  //
  // URL paths (respect API_BASE_PATH):
  //   Go admin  : GET /api/schedules            PUT /api/schedules/{jt}
  //   Public UI : GET /earlscheibconcord/schedules PUT /earlscheibconcord/schedules/{jt}
  //
  // Mirrors the Templates editor (loadTemplates / buildTemplateCard) — same
  // dirty-dot, Save + Reset, status echo. Numeric input + "(X days)" helper
  // is the only structural difference.

  const schedListURL   = IS_LOCAL_ADMIN
    ? '/api/schedules'
    : `${API_BASE}/schedules`;
  const schedUpsertURL = (jt) => IS_LOCAL_ADMIN
    ? `/api/schedules/${encodeURIComponent(jt)}`
    : `${API_BASE}/schedules/${encodeURIComponent(jt)}`;

  const scheduleState = {
    cards:     {},   // job_type -> { delay_hours, is_override, enabled, label, when }
    bounds:    { min: 1, max: 720 },
    loaded:    false,
  };

  function getSchedulesListEl() {
    return document.getElementById('schedules-list');
  }

  async function loadSchedules(force) {
    const listEl = getSchedulesListEl();
    if (!listEl) return;
    if (scheduleState.loaded && !force) return;

    listEl.innerHTML = '';
    const loading = document.createElement('p');
    loading.className = 'schedules-loading';
    loading.textContent = 'Loading schedules…';
    listEl.appendChild(loading);

    try {
      const resp = await fetch(schedListURL, { cache: 'no-store' });
      if (!resp.ok) {
        listEl.innerHTML = '';
        const err = document.createElement('p');
        err.className = 'schedules-error';
        err.textContent = `Couldn't load schedules (${resp.status}).`;
        listEl.appendChild(err);
        return;
      }
      const data = await resp.json();
      scheduleState.bounds = {
        min: typeof data.min_hours === 'number' ? data.min_hours : 1,
        max: typeof data.max_hours === 'number' ? data.max_hours : 720,
      };
      scheduleState.cards = {};
      listEl.innerHTML = '';
      (data.job_types || []).forEach((jt) => {
        scheduleState.cards[jt.job_type] = {
          delay_hours: jt.delay_hours,
          is_override: !!jt.is_override,
          enabled:     jt.enabled !== false,  // default true
          label:       jt.label,
          when:        jt.when,
        };
        listEl.appendChild(buildScheduleCard(jt));
      });
      scheduleState.loaded = true;
    } catch (_) {
      listEl.innerHTML = '';
      const err = document.createElement('p');
      err.className = 'schedules-error';
      err.textContent = "Couldn't reach the server. Please retry.";
      listEl.appendChild(err);
    }
  }

  function buildScheduleCard(jt) {
    const tpl = document.getElementById('schedule-card-template');
    const frag = tpl.content.cloneNode(true);
    const article = frag.querySelector('.sched-card');
    article.dataset.jobType = jt.job_type;

    frag.querySelector('.sched-card__title').textContent = jt.label;
    frag.querySelector('.sched-card__when').textContent  = jt.when;

    const badge = frag.querySelector('.sched-card__badge');
    badge.hidden = !jt.is_override;

    const input    = frag.querySelector('.sched-card__input');
    const daysEl   = frag.querySelector('.sched-card__days');
    const saveBtn  = frag.querySelector('.sched-save');
    const resetBtn = frag.querySelector('.sched-reset');
    const dirtyDot = frag.querySelector('.sched-card__dirty-dot');
    const statusEl = frag.querySelector('.sched-card__status');

    // UKK-07: per-schedule enable/disable toggle. Renders initial state
    // from jt.enabled (default true if missing). Card-level dim via
    // .is-disabled. Hours input + Save + Reset stay interactive even
    // when disabled (locked decision 3).
    const toggleInput = frag.querySelector('.sched-card__toggle-input');
    const toggleLabel = frag.querySelector('.sched-card__toggle-label');
    const enabled = jt.enabled !== false;
    toggleInput.checked = enabled;
    toggleLabel.textContent = enabled ? 'Enabled' : 'Disabled';
    article.classList.toggle('is-disabled', !enabled);

    input.min  = String(scheduleState.bounds.min);
    input.max  = String(scheduleState.bounds.max);
    input.value = String(jt.delay_hours);
    daysEl.textContent = hoursToDaysLabel(jt.delay_hours);
    resetBtn.disabled = !jt.is_override;

    const refreshDirty = () => {
      const saved = scheduleState.cards[jt.job_type] || { delay_hours: 0 };
      const cur = parseInt(input.value, 10);
      const isDirty = Number.isFinite(cur) && cur !== saved.delay_hours;
      dirtyDot.hidden = !isDirty;
      saveBtn.disabled = !isDirty;
      resetBtn.disabled = !saved.is_override;
      statusEl.textContent = '';
      statusEl.removeAttribute('data-state');
    };

    input.addEventListener('input', () => {
      const cur = parseInt(input.value, 10);
      daysEl.textContent = hoursToDaysLabel(cur);
      refreshDirty();
    });

    // Save: PUT {"delay_hours": N}
    saveBtn.addEventListener('click', async () => {
      const cur = parseInt(input.value, 10);
      if (!Number.isFinite(cur)
          || cur < scheduleState.bounds.min
          || cur > scheduleState.bounds.max) {
        showStatus(statusEl,
          `Must be ${scheduleState.bounds.min}–${scheduleState.bounds.max} hours`,
          'error');
        return;
      }
      saveBtn.disabled = true;
      resetBtn.disabled = true;
      try {
        const resp = await fetch(schedUpsertURL(jt.job_type), {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ delay_hours: cur }),
        });
        const parsed = await resp.json().catch(() => ({}));
        if (!resp.ok) {
          showStatus(statusEl, parsed.error || `Save failed (${resp.status})`, 'error');
          saveBtn.disabled = false;
          resetBtn.disabled = !(scheduleState.cards[jt.job_type] || {}).is_override;
          return;
        }
        applySavedSchedule(jt.job_type, parsed, input, daysEl, badge,
                           dirtyDot, saveBtn, resetBtn, statusEl);
      } catch (_) {
        showStatus(statusEl, 'Network error — please retry', 'error');
        saveBtn.disabled = false;
        resetBtn.disabled = !(scheduleState.cards[jt.job_type] || {}).is_override;
      }
    });

    // Reset to default: PUT {} so server reverts.
    resetBtn.addEventListener('click', async () => {
      if (!window.confirm('Restore the default delay for this follow-up?')) return;
      saveBtn.disabled = true;
      resetBtn.disabled = true;
      try {
        const resp = await fetch(schedUpsertURL(jt.job_type), {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: '{}',
        });
        const parsed = await resp.json().catch(() => ({}));
        if (!resp.ok) {
          showStatus(statusEl, parsed.error || `Reset failed (${resp.status})`, 'error');
          saveBtn.disabled = false;
          resetBtn.disabled = false;
          return;
        }
        // Reset reverts both fields server-side; the toggle visual must follow.
        const newEnabled = parsed.enabled !== false;
        toggleInput.checked = newEnabled;
        toggleLabel.textContent = newEnabled ? 'Enabled' : 'Disabled';
        article.classList.toggle('is-disabled', !newEnabled);
        applySavedSchedule(jt.job_type, parsed, input, daysEl, badge,
                           dirtyDot, saveBtn, resetBtn, statusEl);
      } catch (_) {
        showStatus(statusEl, 'Network error — please retry', 'error');
        saveBtn.disabled = false;
        resetBtn.disabled = false;
      }
    });

    // UKK-07: toggle-change handler. PUTs {enabled: bool} immediately. On
    // success: updates label + dim class + cached state, status echoes
    // cancelled-job count when toggling off.
    toggleInput.addEventListener('change', async () => {
      const next = toggleInput.checked;
      toggleInput.disabled = true;
      try {
        const resp = await fetch(schedUpsertURL(jt.job_type), {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ enabled: next }),
        });
        const parsed = await resp.json().catch(() => ({}));
        if (!resp.ok) {
          // Revert checkbox on failure.
          toggleInput.checked = !next;
          toggleLabel.textContent = !next ? 'Enabled' : 'Disabled';
          article.classList.toggle('is-disabled', next);
          showStatus(statusEl, parsed.error || `Toggle failed (${resp.status})`, 'error');
          return;
        }
        // Reflect server state.
        const newEnabled = parsed.enabled !== false;
        toggleInput.checked = newEnabled;
        toggleLabel.textContent = newEnabled ? 'Enabled' : 'Disabled';
        article.classList.toggle('is-disabled', !newEnabled);

        // Update cached state.
        const prev = scheduleState.cards[jt.job_type] || {};
        scheduleState.cards[jt.job_type] = {
          ...prev,
          enabled:     newEnabled,
          is_override: !!parsed.is_override,
        };
        badge.hidden = !parsed.is_override;

        // Status echo per locked decision 4.
        const cancelled = typeof parsed.cancelled_jobs === 'number'
          ? parsed.cancelled_jobs : 0;
        let msg;
        if (newEnabled) {
          msg = 'Enabled';
        } else {
          msg = cancelled > 0
            ? `Disabled · ${cancelled} pending job${cancelled === 1 ? '' : 's'} cancelled`
            : 'Disabled';
        }
        showStatus(statusEl, msg, 'ok');
      } catch (_) {
        toggleInput.checked = !next;
        toggleLabel.textContent = !next ? 'Enabled' : 'Disabled';
        article.classList.toggle('is-disabled', next);
        showStatus(statusEl, 'Network error — please retry', 'error');
      } finally {
        toggleInput.disabled = false;
      }
    });

    return frag;
  }

  function applySavedSchedule(jobType, parsed, input, daysEl, badge,
                              dirtyDot, saveBtn, resetBtn, statusEl) {
    const newDelay = typeof parsed.delay_hours === 'number'
      ? parsed.delay_hours
      : parseInt(input.value, 10);
    input.value = String(newDelay);
    daysEl.textContent = hoursToDaysLabel(newDelay);

    const prev = scheduleState.cards[jobType] || {};
    const newEnabled = typeof parsed.enabled === 'boolean'
      ? parsed.enabled
      : (prev.enabled !== false);
    scheduleState.cards[jobType] = {
      delay_hours: newDelay,
      is_override: !!parsed.is_override,
      enabled:     newEnabled,
      label:       prev.label,
      when:        prev.when,
    };

    badge.hidden = !parsed.is_override;
    dirtyDot.hidden = true;
    saveBtn.disabled = true;
    resetBtn.disabled = !parsed.is_override;

    const rebased = typeof parsed.rebased_jobs === 'number' ? parsed.rebased_jobs : 0;
    let msg;
    if (parsed.is_override) {
      msg = rebased > 0
        ? `Saved · ${rebased} pending job${rebased === 1 ? '' : 's'} rebased`
        : 'Saved';
    } else {
      msg = rebased > 0
        ? `Reverted · ${rebased} pending job${rebased === 1 ? '' : 's'} rebased`
        : 'Reverted to default';
    }
    showStatus(statusEl, msg, 'ok');
  }

  function wireTopnav() {
    const links = document.querySelectorAll('.topnav-link');
    const viewQueue = document.getElementById('view-queue');
    const viewTpl   = document.getElementById('view-templates');
    const viewSched = document.getElementById('view-schedules');
    const viewLogs  = document.getElementById('view-logs');
    if (!links.length || !viewQueue || !viewTpl) return;

    const activate = (target) => {
      links.forEach((a) => {
        const on = a.getAttribute('data-view') === target;
        a.classList.toggle('is-active', on);
        a.setAttribute('aria-selected', on ? 'true' : 'false');
      });
      viewQueue.hidden = target !== 'queue';
      viewTpl.hidden   = target !== 'templates';
      if (viewSched) viewSched.hidden = target !== 'schedules';
      if (viewLogs)  viewLogs.hidden  = target !== 'logs';
      if (target === 'templates') loadTemplates(false);
      if (target === 'schedules') loadSchedules(false);
      if (target === 'logs')      loadSmsLog();
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
    if (window.location.hash === '#schedules') activate('schedules');
    if (window.location.hash === '#logs')      activate('logs');
  }

  // ---------- Logs view (GLV-01) --------------------------------------

  // Filter state for the Logs view. Mirrors the Queue's pill pattern.
  // Valid values: 'all' | 'sent' | 'failed'.
  let currentLogsFilter = 'all';
  let logsRefreshTimerId = null;

  // The sms-log read endpoint accepts POST + HMAC-or-basic — auth gate is
  // identical to /queue. Use POST with empty body so HMAC signing of "" works
  // identically to the existing endpoints.
  const smsLogURL = IS_LOCAL_ADMIN
    ? '/api/sms-log'
    : `${API_BASE}/sms-log`;

  async function loadSmsLog() {
    const listEl = document.getElementById('logs-list');
    if (!listEl) return;
    try {
      const url = `${smsLogURL}?limit=200&status=${encodeURIComponent(currentLogsFilter)}`;
      const resp = await fetch(url, {
        method: 'POST',
        body: '',
        headers: { 'Content-Type': 'application/json' },
      });
      if (!resp.ok) {
        listEl.innerHTML = `<div class="logs-empty">Failed to load (HTTP ${resp.status}).</div>`;
        return;
      }
      const data = await resp.json();
      renderSmsLog(data.rows || []);
    } catch (exc) {
      listEl.innerHTML = `<div class="logs-empty">Failed to load: ${escapeHTML(String(exc))}.</div>`;
    }
  }

  function renderSmsLog(rows) {
    const listEl = document.getElementById('logs-list');
    if (!listEl) return;
    if (!rows.length) {
      listEl.innerHTML = '<div class="logs-empty">No send attempts logged yet.</div>';
      return;
    }
    const frag = document.createDocumentFragment();
    rows.forEach((row) => {
      const div = document.createElement('div');
      div.className = 'log-row';
      const ts = new Date((row.created_at || 0) * 1000);
      const tsStr = isFinite(ts.getTime())
        ? `${ts.toLocaleDateString()} ${ts.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' })}`
        : '—';
      const status = (row.status || 'failed').toLowerCase();
      const kindText = (row.kind || 'send').toLowerCase();
      const jobType = row.job_type || '—';
      const phone = row.phone || '—';
      const body = row.body || '';
      const err = row.error ? `<small>${escapeHTML(row.error)}</small>` : '';
      const testTag = row.is_test
        ? '<span class="log-row__test-tag">test</span>'
        : '';
      div.innerHTML = `
        <div class="log-row__ts">${escapeHTML(tsStr)}</div>
        <div class="log-row__status" data-status="${escapeAttr(status)}">${escapeHTML(status)}</div>
        <div class="log-row__kind">${escapeHTML(kindText)}${testTag}</div>
        <div>
          <div class="log-row__phone">${escapeHTML(phone)}</div>
          <div class="log-row__job-type">${escapeHTML(jobType)}</div>
        </div>
        <div class="log-row__body">${escapeHTML(body)}${err}</div>
      `;
      frag.appendChild(div);
    });
    listEl.innerHTML = '';
    listEl.appendChild(frag);
  }

  function escapeHTML(s) {
    return String(s)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;')
      .replace(/'/g, '&#39;');
  }
  function escapeAttr(s) { return escapeHTML(s); }

  function wireLogsFilters() {
    document.querySelectorAll('.logs-filter').forEach((btn) => {
      btn.addEventListener('click', () => {
        const f = btn.getAttribute('data-logs-filter') || 'all';
        if (f === currentLogsFilter) return;
        currentLogsFilter = f;
        document.querySelectorAll('.logs-filter').forEach((b) => {
          b.setAttribute(
            'aria-selected',
            b.getAttribute('data-logs-filter') === f ? 'true' : 'false',
          );
        });
        loadSmsLog();
      });
    });
  }

  // ---------- Wire up --------------------------------------------------

  document.addEventListener('DOMContentLoaded', () => {
    wireFilters();
    wireSearch();
    wireTopnav();
    wireLogsFilters();

    // GLV-01: when the Logs tab is visible, auto-refresh on the same cadence
    // as the queue so a fresh send shows up without a manual reload. Skip
    // the poll when the tab is hidden to keep the network quiet.
    setInterval(() => {
      const viewLogs = document.getElementById('view-logs');
      if (viewLogs && !viewLogs.hidden) loadSmsLog();
    }, REFRESH_MS);

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
        // Hydrate SHOP_CONSTANTS from the server's authoritative values
        // so the queue-card preview renders the same shop_name / shop_phone /
        // review_url that the actual SMS will use. Without this, the hardcoded
        // JS fallbacks (e.g. "Earl Scheib Auto Body Concord") drift from the
        // server's SHOP_CONSTANTS on a rename. data.sample_row has the shop
        // placeholders merged in (see app.py /templates handler).
        const shopKeys = (data.placeholders && data.placeholders.shop) || [];
        const sample = data.sample_row || {};
        shopKeys.forEach((k) => {
          if (sample[k] !== undefined && sample[k] !== null && sample[k] !== '') {
            SHOP_CONSTANTS[k] = sample[k];
          }
        });
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
