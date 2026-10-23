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
    'appt_reminder':
      'Hi {first_name}, a friendly reminder from {shop_name}: your ' +
      '{year} {short_model} is booked for {appt_time}. See you then! ' +
      'Questions? Call {shop_phone}.',
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
    // RMD-01: {appt_time} from the row's active booking ("Wed, Aug 5 at
    // 9:00 AM") — mirrors app.py render_template.
    if (!bag.appt_time && bag.appointment_at) {
      const d = new Date(bag.appointment_at * 1000);
      bag.appt_time = d.toLocaleDateString('en-US', {
        weekday: 'short', month: 'short', day: 'numeric',
      }) + ' at ' + d.toLocaleTimeString('en-US', {
        hour: 'numeric', minute: '2-digit',
      });
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
    'appt_reminder': 'Appointment reminder',
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
  // CAL-04/05: Google Calendar state from /diagnostic. The manual
  // 'Add to Google Calendar' link is hidden only while the sync is actually
  // running (connected AND the Scheduled-view toggle is on).
  let gcalConnected = false;
  let gcalEnabled   = true;
  const gcalSyncActive = () => gcalConnected && gcalEnabled;
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
      `${j.id}:${j.sent || 0}:${j.send_at || 0}:${j.sent_at || 0}:${j.send_at_manual || 0}:${j.booked || 0}:${j.appointment_at || 0}`,
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

  // SCH-01: manual scheduling can push send times days out, where a bare
  // "2:30 PM" is ambiguous. Same-Pacific-day → time only (original
  // behaviour); any other day → "Aug 12, 2:30 PM".
  const dateTimeFmt = new Intl.DateTimeFormat('en-US', {
    timeZone: 'America/Los_Angeles',
    month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit',
  });
  const dayKeyFmt = new Intl.DateTimeFormat('en-CA', {
    timeZone: 'America/Los_Angeles',
    year: 'numeric', month: '2-digit', day: '2-digit',
  });

  // Copy rule (spec 4.4): "Today 2:30 PM", "Tomorrow 9:00 AM",
  // "Tue, Oct 27 · 10:30 AM" (year only when it differs from this year).
  const weekdayDateFmt = new Intl.DateTimeFormat('en-US', {
    timeZone: 'America/Los_Angeles', weekday: 'short', month: 'short', day: 'numeric',
  });
  const weekdayDateYearFmt = new Intl.DateTimeFormat('en-US', {
    timeZone: 'America/Los_Angeles', weekday: 'short', month: 'short', day: 'numeric', year: 'numeric',
  });
  const yearFmt = new Intl.DateTimeFormat('en-US', { timeZone: 'America/Los_Angeles', year: 'numeric' });
  function formatWhen(unixSeconds) {
    if (!unixSeconds) return '';
    const d = new Date(unixSeconds * 1000);
    const now = new Date();
    const dayKey = dayKeyFmt.format(d);
    if (dayKey === dayKeyFmt.format(now)) return `Today ${timeFmt.format(d)}`;
    if (dayKey === dayKeyFmt.format(new Date(now.getTime() + 86400000))) return `Tomorrow ${timeFmt.format(d)}`;
    if (dayKey === dayKeyFmt.format(new Date(now.getTime() - 86400000))) return `Yesterday ${timeFmt.format(d)}`;
    const dateFmt = yearFmt.format(d) === yearFmt.format(now) ? weekdayDateFmt : weekdayDateYearFmt;
    return `${dateFmt.format(d)} · ${timeFmt.format(d)}`;
  }
  function formatAbsolute(unixSeconds) { return formatWhen(unixSeconds); }

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
        // BOK-01: booked estimates leave the pending view — their pre-work
        // follow-ups are suppressed and they live under Scheduled instead.
        return !isTest && (job.job_type === '24h' || job.job_type === '3day')
          && isPending && job.booked !== 1;
      case 'completed':
        // Review requests are NOT suppressed by booking (they fire after
        // the work is done), so no booked check here.
        return !isTest && job.job_type === 'review' && isPending;
      // BOK-01: Scheduled = the customer committed to the work. Shows every
      // row of a booked estimate (any state) so the full timeline reads.
      case 'scheduled':
        return !isTest && job.booked === 1;
      case 'sent':
        return !isTest && job.sent === 1 && !isCancelled;
      case 'cancelled':
        return !isTest && isCancelled;
      case 'test-estimates':
        return isTest && (job.job_type === '24h' || job.job_type === '3day')
          && isPending && job.booked !== 1;
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
          // BOK-01: per-estimate booking state (same value on every row).
          booked:         job.booked === 1 ? 1 : 0,
          appointment_at: job.appointment_at || 0,
          gcal_synced:    job.gcal_synced === 1 ? 1 : 0,   // CAL-05: already on his calendar
          is_test:        job.is_test === 1 ? 1 : 0,
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
    // BOK-01: month calendar rides above the cards on the Scheduled view.
    // Rendered outside the renderKey skip so filter flips always toggle it.
    renderCalendar();
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
        const EMPTY = {
          estimates: ['No open estimates', 'New estimates from CCC show up here within a minute.'],
          completed: ['No finished work waiting', 'When a repair is marked complete in CCC, the review request appears here.'],
          scheduled: ['No booked appointments yet', 'When a customer books their work, tap "Mark scheduled" on their card. It moves here and their follow-up texts stop.'],
          sent: ['Nothing sent yet', 'Texts that went out show up here with the time they were sent.'],
          cancelled: ['No cancelled follow-ups', 'Texts you cancel land here. You can restore one any time.'],
          'test-estimates': ['No test estimates', 'Use the reset button to seed fresh test jobs.'],
          'test-completed': ['No test work completed', 'Use the reset button to seed fresh test jobs.'],
        };
        if (needle) {
          titleEl.textContent = 'No matches';
          if (subEl) subEl.textContent = `Nothing here matches "${currentSearch.trim()}". Try a name, phone, VIN or RO number.`;
        } else if (EMPTY[currentFilter]) {
          titleEl.textContent = EMPTY[currentFilter][0];
          if (subEl) subEl.textContent = EMPTY[currentFilter][1];
        } else {
          titleEl.textContent = 'Nothing here';
          if (subEl) subEl.textContent = 'No jobs in this view right now.';
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
      const sameYear = d.getFullYear() === new Date().getFullYear();
      dateEl.textContent = 'Estimate ' + d.toLocaleDateString('en-US',
        sameYear ? { month: 'short', day: 'numeric' } : { month: 'short', day: 'numeric', year: 'numeric' });
      dateEl.title = 'Estimate written ' + d.toLocaleDateString('en-US', { month: 'long', day: 'numeric', year: 'numeric' });
    }

    wireBookingUI(frag, group);

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

    // One primary action per card: the first pending "Send now" is red,
    // any further pending ones step down to secondary.
    const sendBtns = timeline.querySelectorAll(
      '.timeline__entry[data-state="pending"]:not([data-suppressed]) .send-now-btn:not([hidden])',
    );
    sendBtns.forEach((b, i) => {
      if (i > 0) { b.classList.remove('btn-primary'); b.classList.add('btn-secondary'); }
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

    // "Sends Tue, Oct 27 · 8:00 AM" with the countdown as quiet meta.
    const sendEl = frag.querySelector('.job-send');
    const overdue = job.send_at && job.send_at < Date.now() / 1000;
    frag.querySelector('.job-send-relative').textContent = overdue ? 'Overdue' : 'Sends';
    frag.querySelector('.job-send-absolute').textContent = formatWhen(job.send_at);
    const inEl = frag.querySelector('.job-send-in');
    if (inEl) inEl.textContent = overdue ? '' : formatRelative(job.send_at);
    if (sendEl) sendEl.classList.toggle('job-send--overdue', !!overdue);

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
      stampEl.textContent = 'Sent ' + formatWhen(whenTs);
      frag.querySelector('.job-send').hidden = true;
    } else if (job.booked === 1
               && (job.job_type === '24h' || job.job_type === '3day')) {
      // BOK-01 + BOK-04 (per Marco): pre-work follow-ups are CANCELLED
      // once the customer books — present them exactly like cancelled
      // rows (label, no action buttons, faded preview). The scheduler
      // was already skipping them; this makes the UI say so. They come
      // back automatically if the booking is unmarked.
      const stampEl = frag.querySelector('.timeline__sent-stamp');
      stampEl.hidden = false;
      stampEl.textContent = 'Cancelled · customer booked';
      frag.querySelector('.job-send').hidden = true;
      li.dataset.suppressed = '1';
      const sBtn = frag.querySelector('.send-now-btn');
      const cBtn = frag.querySelector('.cancel-btn');
      if (sBtn) sBtn.hidden = true;
      if (cBtn) cBtn.hidden = true;
    }

    frag.querySelector('.sms-bubble').textContent = previewSMS(job.job_type, job);

    const sendBtn     = frag.querySelector('.send-now-btn');
    const cancelBtn   = frag.querySelector('.cancel-btn');
    const resendBtn   = frag.querySelector('.resend-btn');
    const uncancelBtn = frag.querySelector('.uncancel-btn');
    const scheduleBtn = frag.querySelector('.schedule-btn');
    const schedForm   = frag.querySelector('.schedule-form');
    const errEl       = frag.querySelector('.job-error');

    // SCH-01: "Scheduled by you" tag on pending rows Marco hand-scheduled.
    const manualTag = frag.querySelector('.manual-tag');
    if (manualTag && job.send_at_manual === 1
        && job.sent !== 1 && job.cancelled !== 1) {
      manualTag.hidden = false;
    }

    // GLV-01 + GLV-02 + GLV-04 + SCH-01: pick the right action buttons
    // per state.
    //   pending   → Send-now + Schedule + Cancel
    //   sent      → Resend
    //   cancelled → Uncancel
    if (job.cancelled === 1) {
      if (sendBtn)     sendBtn.hidden     = true;
      if (cancelBtn)   cancelBtn.hidden   = true;
      if (resendBtn)   resendBtn.hidden   = true;
      if (uncancelBtn) uncancelBtn.hidden = false;
      if (scheduleBtn) scheduleBtn.hidden = true;
    } else if (job.sent === 1) {
      if (sendBtn)     sendBtn.hidden     = true;
      if (cancelBtn)   cancelBtn.hidden   = true;
      if (resendBtn)   resendBtn.hidden   = false;
      if (uncancelBtn) uncancelBtn.hidden = true;
      if (scheduleBtn) scheduleBtn.hidden = true;
    } else {
      if (resendBtn)   resendBtn.hidden   = true;
      if (uncancelBtn) uncancelBtn.hidden = true;
      // SCH-01: the Go local admin has no /api/schedule proxy — Pi-only
      // feature, same pattern as the auto-send toggle.
      if (scheduleBtn && IS_LOCAL_ADMIN) scheduleBtn.hidden = true;
    }

    if (sendBtn)     sendBtn.addEventListener('click',     () => handleSendNow(job, li, sendBtn, errEl));
    if (cancelBtn)   cancelBtn.addEventListener('click',   () => handleCancel(job, li, cancelBtn, errEl));
    if (resendBtn)   resendBtn.addEventListener('click',   () => handleResend(job, li, resendBtn, errEl));
    if (uncancelBtn) uncancelBtn.addEventListener('click', () => handleUncancel(job, li, uncancelBtn, errEl));
    if (scheduleBtn && schedForm) {
      scheduleBtn.addEventListener('click', () => toggleScheduleForm(job, schedForm));
      const saveBtn  = schedForm.querySelector('.schedule-save-btn');
      const closeBtn = schedForm.querySelector('.schedule-close-btn');
      if (closeBtn) closeBtn.addEventListener('click', () => { schedForm.hidden = true; });
      if (saveBtn)  saveBtn.addEventListener('click', () => handleSchedule(job, schedForm, saveBtn, errEl));
    }

    return frag;
  }

  function updateStats() {
    statPendingEl.textContent = String(counters.pending);
    statSentEl.textContent    = String(counters.sentToday);
    statFailedEl.textContent  = String(counters.failed);
    statFailedEl.classList.toggle('is-bad', counters.failed > 0);
  }

  // ---------- Send-now flow -------------------------------------------

  // Errors Marco reads: what happened and what to do, in the app's voice.
  // The server's reason is kept for the console; it only reaches the screen
  // when it is already a sentence (not a snake_case tag or an HTTP code).
  function friendlyError(what, parsed, status) {
    const reason = parsed && parsed.error ? String(parsed.error) : '';
    console.warn(`${what} failed`, status, reason);
    if (reason && /\s/.test(reason) && !/^http/i.test(reason)) return `${what} didn't go through: ${reason}`;
    return `${what} didn't go through. Try again.`;
  }
  const NETWORK_ERROR = "Can't reach the server. Check your connection and try again.";

  async function handleSendNow(job, entryEl, btnEl, errEl) {
    const type = (JOB_TYPE_LABELS[job.job_type] || job.job_type).toLowerCase();
    const confirmMsg = job.is_test === 1
      ? `Send the ${type} text to the test phones (Jaskarn and Marco) now?`
      : `Send the ${type} text to ${job.name || 'this customer'} now?`;
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
        showEntryError(errEl, 'This text was already sent or cancelled. Refreshing…');
        setTimeout(fetchQueue, 600);
        return;
      }

      const parsed = await resp.json().catch(() => ({}));
      counters.failed++;
      updateStats();
      showEntryError(errEl, friendlyError('The text', parsed, resp.status));
      btnEl.disabled = false;

    } catch (e) {
      counters.failed++;
      updateStats();
      showEntryError(errEl, NETWORK_ERROR);
      btnEl.disabled = false;
    }
  }

  // ---------- Custom date+time picker (BOK-02) -------------------------

  // Shop-hours drop-off slots: 8:00 AM – 5:30 PM in 30-minute steps.
  const DTP_SLOTS = (() => {
    const out = [];
    for (let mins = 8 * 60; mins <= 17 * 60 + 30; mins += 30) out.push(mins);
    return out;
  })();

  function fmtSlot(mins) {
    const h24 = Math.floor(mins / 60);
    const mm = mins % 60;
    const h12 = ((h24 + 11) % 12) + 1;
    return `${h12}:${String(mm).padStart(2, '0')} ${h24 < 12 ? 'AM' : 'PM'}`;
  }

  function openDTP(formEl, seedTs) {
    const seed = new Date(seedTs * 1000);
    const st = {
      y: seed.getFullYear(), m: seed.getMonth(), day: seed.getDate(),
      mins: seed.getHours() * 60 + seed.getMinutes(),
      viewY: seed.getFullYear(), viewM: seed.getMonth(),
    };
    if (!DTP_SLOTS.includes(st.mins)) st.mins = 9 * 60;
    formEl._dtp = st;
    renderDTP(formEl);
  }

  function dtpPickedTs(formEl) {
    const st = formEl._dtp;
    if (!st) return NaN;
    return Math.floor(new Date(
      st.y, st.m, st.day, Math.floor(st.mins / 60), st.mins % 60,
    ).getTime() / 1000);
  }

  function renderDTP(formEl) {
    const st = formEl._dtp;
    const view = new Date(st.viewY, st.viewM, 1);
    formEl.querySelector('.dtp-title').textContent =
      view.toLocaleDateString('en-US', { month: 'long', year: 'numeric' });

    const today = new Date(); today.setHours(0, 0, 0, 0);
    const firstDow = view.getDay();
    const daysInMonth = new Date(st.viewY, st.viewM + 1, 0).getDate();
    const todayD = new Date();

    let grid = ['S', 'M', 'T', 'W', 'T', 'F', 'S']
      .map((d) => `<span class="dtp-dow">${d}</span>`).join('');
    for (let i = 0; i < firstDow; i += 1) grid += '<span class="dtp-pad"></span>';
    for (let day = 1; day <= daysInMonth; day += 1) {
      const cellDate = new Date(st.viewY, st.viewM, day);
      const past = cellDate < today;
      const sel = st.y === st.viewY && st.m === st.viewM && st.day === day;
      const isToday = todayD.getFullYear() === st.viewY
        && todayD.getMonth() === st.viewM && todayD.getDate() === day;
      grid += `<button type="button" class="dtp-day${sel ? ' dtp-day--sel' : ''}`
        + `${isToday ? ' dtp-day--today' : ''}" data-day="${day}"`
        + `${past ? ' disabled' : ''}>${day}</button>`;
    }
    formEl.querySelector('.dtp-grid').innerHTML = grid;

    formEl.querySelector('.dtp-slots').innerHTML = DTP_SLOTS.map((mins) =>
      `<button type="button" class="dtp-slot${mins === st.mins ? ' dtp-slot--sel' : ''}"`
      + ` data-mins="${mins}">${fmtSlot(mins)}</button>`,
    ).join('');

    formEl.querySelector('.dtp-picked').textContent =
      new Date(st.y, st.m, st.day).toLocaleDateString('en-US', {
        weekday: 'short', month: 'short', day: 'numeric',
      }) + ' · ' + fmtSlot(st.mins);
  }

  function wireDTP(formEl) {
    formEl.addEventListener('click', (e) => {
      const st = formEl._dtp;
      if (!st) return;
      const dayBtn  = e.target.closest('.dtp-day:not([disabled])');
      const slotBtn = e.target.closest('.dtp-slot');
      if (e.target.closest('.dtp-prev')) {
        const v = new Date(st.viewY, st.viewM - 1, 1);
        st.viewY = v.getFullYear(); st.viewM = v.getMonth();
      } else if (e.target.closest('.dtp-next')) {
        const v = new Date(st.viewY, st.viewM + 1, 1);
        st.viewY = v.getFullYear(); st.viewM = v.getMonth();
      } else if (dayBtn) {
        st.y = st.viewY; st.m = st.viewM; st.day = Number(dayBtn.dataset.day);
      } else if (slotBtn) {
        st.mins = Number(slotBtn.dataset.mins);
      } else {
        return;
      }
      renderDTP(formEl);
    });
  }

  // ---------- Booking flow (BOK-01) -----------------------------------

  // "Aug 12, 9:00 AM" (Pacific) — appointment dates are usually days out,
  // so always include the date.
  function formatAppt(unixSeconds) { return formatWhen(unixSeconds); }

  // Prefilled Google Calendar event link — creates the event in whatever
  // Google account Marco is signed into. Zero-setup v1 of "connect my
  // Google Calendar"; floating local times (no Z) land in his calendar's
  // own zone (Pacific).
  function gcalHref(group) {
    const fmtG = (unixSeconds) => {
      const d = new Date(unixSeconds * 1000);
      const pad = (n) => String(n).padStart(2, '0');
      return `${d.getFullYear()}${pad(d.getMonth() + 1)}${pad(d.getDate())}`
        + `T${pad(d.getHours())}${pad(d.getMinutes())}00`;
    };
    const title = `Earl Scheib: ${group.name || 'Customer'}`
      + (group.vehicle_desc ? ` (${group.vehicle_desc})` : '');
    const detailLines = [
      'Booked repair work.',
      group.name ? `Customer: ${group.name} ${formatPhone(group.phone)}` : `Customer: ${formatPhone(group.phone)}`,
      group.vehicle_desc ? `Vehicle: ${group.vehicle_desc}` : '',
      group.ro_id ? `RO ${group.ro_id}` : '',
    ].filter(Boolean);
    const params = new URLSearchParams({
      action: 'TEMPLATE',
      text: title,
      dates: `${fmtG(group.appointment_at)}/${fmtG(group.appointment_at + 3600)}`,
      details: detailLines.join('\n'),
      location: 'Earl Scheib Auto Body, Concord CA',
    });
    return `https://calendar.google.com/calendar/render?${params.toString()}`;
  }

  function wireBookingUI(frag, group) {
    const bookingEl = frag.querySelector('.booking');
    if (!bookingEl) return;
    // Pi-only feature (Go local admin has no proxy for /queue/book).
    if (IS_LOCAL_ADMIN) { bookingEl.hidden = true; return; }

    const pill      = bookingEl.querySelector('.booked-pill');
    const whenEl    = bookingEl.querySelector('.booked-when');
    const bookBtn   = bookingEl.querySelector('.book-btn');
    const gcalEl    = bookingEl.querySelector('.gcal-link');
    const unbookBtn = bookingEl.querySelector('.unbook-btn');
    const form      = bookingEl.querySelector('.book-form');
    const errEl     = bookingEl.querySelector('.book-error');

    if (group.booked === 1) {
      pill.hidden = false;
      whenEl.textContent = formatAppt(group.appointment_at);
      bookBtn.querySelector('span').textContent = 'Change date';
      if (gcalSyncActive() || group.gcal_synced === 1) {
        // CAL-04/05: the booking is (or will be) on the calendar via the
        // sync; a manual add would only create a duplicate. While paused,
        // only bookings that never synced get the manual link back.
        gcalEl.hidden = true;
        pill.removeAttribute('href');
        pill.title = gcalSyncActive() ? 'Synced to Google Calendar' : 'Already on your Google Calendar (sync paused)';
      } else {
        gcalEl.hidden = false;
        gcalEl.href = gcalHref(group);
        // BOK-03: tapping the date/time pill itself adds to the calendar.
        pill.href = gcalHref(group);
        pill.title = 'Tap to add to your calendar';
      }
      unbookBtn.hidden = false;
    }

    wireDTP(form);
    bookBtn.addEventListener('click', () => {
      if (!form.hidden) { form.hidden = true; return; }
      const nowSec = Math.floor(Date.now() / 1000);
      // Prefill: existing appointment, else tomorrow 9:00 AM.
      let seed = group.appointment_at;
      if (!seed || seed < nowSec - 86400) {
        const t = new Date();
        t.setDate(t.getDate() + 1);
        t.setHours(9, 0, 0, 0);
        seed = Math.floor(t.getTime() / 1000);
      }
      openDTP(form, seed);
      form.hidden = false;
    });
    form.querySelector('.book-close-btn').addEventListener('click', () => { form.hidden = true; });
    form.querySelector('.book-save-btn').addEventListener('click', () => handleBook(group, form, errEl));
    unbookBtn.addEventListener('click', () => handleUnbook(group, unbookBtn, errEl));

    // CAN-01: card-level cancel of all pending follow-ups (24h/3day), one confirm.
    const cancelAllBtn = bookingEl.querySelector('.cancel-all-btn');
    if (cancelAllBtn) {
      const pending = (group.jobs || []).filter((j) => j.sent !== 1 && j.cancelled !== 1
        && (j.job_type === '24h' || j.job_type === '3day'));
      cancelAllBtn.hidden = pending.length === 0;
      cancelAllBtn.addEventListener('click', () => handleCancelAll(group, pending, cancelAllBtn, errEl));
    }
  }

  // MSG-02: the confirmation text drafted after Mark scheduled / Change
  // date. Same voice as the appointment reminder template; Marco can edit
  // it before he sends. Time is shop time (Pacific), like {appt_time}.
  function bookingConfirmText(group, ts, isReschedule) {
    const raw = (group.name || '').trim().split(/\s+/)[0] || '';
    const first = raw ? raw.charAt(0).toUpperCase() + raw.slice(1).toLowerCase() : '';
    const car = (group.vehicle_desc || '').trim().split(/\s+/).slice(0, 3).join(' ');
    const when = new Date(ts * 1000).toLocaleString('en-US', {
      timeZone: 'America/Los_Angeles', weekday: 'short', month: 'short', day: 'numeric',
      hour: 'numeric', minute: '2-digit',
    }).replace(/, (\d{1,2}:\d{2})/, ' at $1');
    const hi = first ? `Hi ${first}, ` : 'Hi, ';
    const what = car ? `your ${car}` : 'your vehicle';
    return isReschedule
      ? `${hi}this is Earl Scheib Of Concord. We've moved ${what} to ${when}. See you then! Questions? Call (925) 609-7780.`
      : `${hi}this is Earl Scheib Of Concord. ${what.charAt(0).toUpperCase() + what.slice(1)} is booked for ${when}. See you then! Questions? Call (925) 609-7780.`;
  }

  async function handleBook(group, formEl, errEl) {
    const btnEl = formEl.querySelector('.book-save-btn');
    const ts = dtpPickedTs(formEl);
    if (!Number.isFinite(ts)) { showEntryError(errEl, 'Pick the appointment date first.'); return; }

    btnEl.disabled = true;
    errEl.hidden = true; errEl.textContent = '';
    try {
      const resp = await fetch(`${API_BASE}/queue/book`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ estimate_key: group.key, appointment_at: ts }),
      });
      if (resp.status === 200) {
        btnEl.textContent = 'Booked';
        // MSG-02 (per Marco): after booking or rebooking, open the
        // customer's Messages thread with the confirmation text typed in,
        // so he only taps Send.
        if (group.phone && group.is_test !== 1 && !IS_LOCAL_ADMIN) {
          try {
            sessionStorage.setItem('esw-draft', JSON.stringify({
              phone: group.phone,
              name: group.name || '',
              text: bookingConfirmText(group, ts, group.booked === 1),
              at: Date.now(),
            }));
            setTimeout(() => {
              window.location.href = `/earlscheib/messages#${encodeURIComponent(group.phone)}`;
            }, 700);
            return;
          } catch (e) { /* no sessionStorage: stay on the card */ }
        }
        setTimeout(fetchQueue, 600);
        setTimeout(() => {
          formEl.hidden = true;
          btnEl.disabled = false;
          btnEl.textContent = 'Save appointment';
        }, 1400);
        return;
      }
      const parsed = await resp.json().catch(() => ({}));
      showEntryError(errEl, friendlyError('Saving the appointment', parsed, resp.status));
      btnEl.disabled = false;
    } catch (e) {
      showEntryError(errEl, NETWORK_ERROR);
      btnEl.disabled = false;
    }
  }

  async function handleCancelAll(group, pending, btnEl, errEl) {
    const who = group.name || formatPhone(group.phone) || 'this customer';
    const n = pending.length;
    if (!n) return;
    const first = (who.split(/\s+/)[0]) || who;
    if (!window.confirm(`Cancel ${n === 1 ? 'the follow-up' : `all ${n} follow-ups`} for ${first}? Their review request after the work still goes out.`)) return;
    btnEl.disabled = true;
    errEl.hidden = true; errEl.textContent = '';
    const failed = [];
    for (const job of pending) {
      try {
        const resp = await fetch(`${API_BASE}/queue`, {
          method: 'DELETE',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ id: job.id }),
        });
        if (!resp.ok) failed.push(job.job_type);
      } catch (err) {
        failed.push(job.job_type);
      }
    }
    if (failed.length) {
      errEl.textContent = `Couldn't cancel ${failed.length === 1 ? 'one of the texts' : 'some of the texts'}. Try the Cancel button on each one.`;
      errEl.hidden = false;
      btnEl.disabled = false;
    } else {
      btnEl.hidden = true;
    }
    fetchQueue();
  }

  async function handleUnbook(group, btnEl, errEl) {
    const who = group.name || formatPhone(group.phone) || 'this customer';
    if (!window.confirm(`Unmark ${who} as scheduled? Their follow-up texts start again.`)) return;
    btnEl.disabled = true;
    errEl.hidden = true; errEl.textContent = '';
    try {
      const resp = await fetch(`${API_BASE}/queue/unbook`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ estimate_key: group.key }),
      });
      if (resp.status === 200) { setTimeout(fetchQueue, 400); return; }
      const parsed = await resp.json().catch(() => ({}));
      showEntryError(errEl, friendlyError('Unmarking', parsed, resp.status));
      btnEl.disabled = false;
    } catch (e) {
      showEntryError(errEl, NETWORK_ERROR);
      btnEl.disabled = false;
    }
  }

  // ---------- Appointment calendar (BOK-01) ----------------------------

  const calEl      = document.getElementById('sched-calendar');
  const calGridEl  = document.getElementById('cal-grid');
  const calTitleEl = document.getElementById('cal-title');
  // First day of the displayed month, local time.
  let calMonth = (() => { const d = new Date(); return new Date(d.getFullYear(), d.getMonth(), 1); })();

  function activeAppointments() {
    const seen = new Set();
    const out = [];
    (lastJobs || []).forEach((j) => {
      if (j.booked !== 1 || !j.appointment_at || j.is_test === 1) return;
      if (seen.has(j.estimate_key)) return;
      seen.add(j.estimate_key);
      out.push({
        key: j.estimate_key,
        name: j.name || formatPhone(j.phone),
        at: j.appointment_at,
      });
    });
    return out;
  }

  function renderCalendar() {
    if (!calEl) return;
    if (currentFilter !== 'scheduled') { calEl.hidden = true; return; }
    calEl.hidden = false;

    const appts = activeAppointments();
    const byDay = new Map();   // "YYYY-M-D" (local) -> [{name, at}]
    appts.forEach((a) => {
      const d = new Date(a.at * 1000);
      const k = `${d.getFullYear()}-${d.getMonth()}-${d.getDate()}`;
      if (!byDay.has(k)) byDay.set(k, []);
      byDay.get(k).push(a);
    });

    const y = calMonth.getFullYear(), m = calMonth.getMonth();
    calTitleEl.textContent = calMonth.toLocaleDateString('en-US', { month: 'long', year: 'numeric' });

    const today = new Date();
    const firstDow = new Date(y, m, 1).getDay();
    const daysInMonth = new Date(y, m + 1, 0).getDate();

    let html = ['Sun','Mon','Tue','Wed','Thu','Fri','Sat']
      .map((d) => `<div class="cal-dow">${d}</div>`).join('');
    for (let i = 0; i < firstDow; i += 1) html += '<div class="cal-cell cal-cell--pad"></div>';
    for (let day = 1; day <= daysInMonth; day += 1) {
      const k = `${y}-${m}-${day}`;
      const isToday = today.getFullYear() === y && today.getMonth() === m && today.getDate() === day;
      const dayAppts = (byDay.get(k) || []).slice().sort((a, b) => a.at - b.at);
      const chips = dayAppts.map((a) =>
        `<button type="button" class="cal-appt" data-key="${escapeHTML(a.key)}" title="${escapeHTML(a.name)} — ${escapeHTML(formatAppt(a.at))}">`
        + `<span class="cal-appt-time mono">${escapeHTML(timeFmt.format(new Date(a.at * 1000)))}</span> `
        + `${escapeHTML(a.name)}</button>`,
      ).join('');
      html += `<div class="cal-cell${isToday ? ' cal-cell--today' : ''}${dayAppts.length ? ' cal-cell--busy' : ''}">`
        + `<span class="cal-day">${day}</span>${chips}</div>`;
    }
    calGridEl.innerHTML = html;
    refreshApptPopup();
  }

  function escapeHTML(s) {
    return String(s == null ? '' : s).replace(/[&<>"']/g, (c) => (
      { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]
    ));
  }

  // ---------- Google Calendar sync (CAL-04 / CAL-05) -------------------
  // Two surfaces, both hidden in the local Go admin and when the Pi has no
  // GCAL_CLIENT_ID (diagnostic.gcal is null):
  //  - #gcal-banner: top-of-page strip with the Connect button, shown only
  //    while not connected (and not snoozed via the x for 7 days);
  //  - #cal-gcal: panel at the top of the Scheduled view with the
  //    pause/resume toggle, Sync now and Disconnect once connected.
  const gcalBar        = document.getElementById('cal-gcal');
  const gcalText       = document.getElementById('cal-gcal-text');
  const gcalConnectEl  = document.getElementById('cal-gcal-connect');
  const gcalSyncBtn    = document.getElementById('cal-gcal-sync');
  const gcalDiscBtn    = document.getElementById('cal-gcal-disconnect');
  const gcalToggleWrap = document.getElementById('cal-gcal-toggle-wrap');
  const gcalToggle     = document.getElementById('cal-gcal-toggle');
  const gcalToggleLbl  = document.getElementById('cal-gcal-toggle-label');
  const gcalBanner     = document.getElementById('gcal-banner');
  const gcalBannerText = document.getElementById('gcal-banner-text');
  const gcalBannerCta  = document.getElementById('gcal-banner-connect');
  const gcalBannerX    = document.getElementById('gcal-banner-close');
  const GCAL_BANNER_SNOOZE_KEY = 'eswGcalBannerHiddenUntil';
  const GCAL_BANNER_TEXT = gcalBannerText ? gcalBannerText.textContent : '';
  let gcalFlash = '';       // one-shot message after the Google redirect
  let gcalFlashKind = '';   // 'ok' | 'error'
  let gcalNotice = '';      // short-lived panel message (e.g. toggle failed)

  function gcalBannerSnoozed() {
    try { return Number(localStorage.getItem(GCAL_BANNER_SNOOZE_KEY) || 0) > Date.now(); }
    catch (_) { return false; }
  }

  function agoText(unixSeconds) {
    if (!unixSeconds) return 'never';
    const s = Math.max(0, Math.floor(Date.now() / 1000) - unixSeconds);
    if (s < 60) return 'just now';
    if (s < 3600) return `${Math.floor(s / 60)} min ago`;
    if (s < 86400) return `${Math.floor(s / 3600)} h ago`;
    return `${Math.floor(s / 86400)} d ago`;
  }

  function renderGcal(g) {
    if (!gcalBar) return;
    if (IS_LOCAL_ADMIN || !g || !g.configured) {
      gcalBar.hidden = true;
      if (gcalBanner) gcalBanner.hidden = true;
      return;
    }
    const wasActive = gcalSyncActive();
    gcalConnected = !!g.connected;
    gcalEnabled   = g.enabled !== false;
    const active  = gcalSyncActive();
    const connectHref = `${API_BASE}/gcal/connect`;

    // Top banner: only while not connected. Hidden once he signs in.
    if (gcalBanner) {
      gcalBanner.hidden = gcalConnected || gcalBannerSnoozed();
      const showErr = !gcalConnected && gcalFlashKind === 'error' && !!gcalFlash;
      gcalBanner.classList.toggle('gcal-banner--error', showErr);
      if (gcalBannerText) gcalBannerText.textContent = showErr ? gcalFlash : GCAL_BANNER_TEXT;
      if (gcalBannerCta) gcalBannerCta.href = connectHref;
    }

    // Scheduled-view panel.
    gcalBar.hidden = false;
    gcalBar.classList.toggle('gsync--error', !!g.last_error);
    gcalBar.classList.toggle('gsync--off', gcalConnected && !gcalEnabled);
    if (gcalConnectEl) { gcalConnectEl.hidden = gcalConnected; gcalConnectEl.href = connectHref; }
    if (gcalToggleWrap) gcalToggleWrap.hidden = !gcalConnected;
    if (gcalToggle && !gcalToggle.disabled) gcalToggle.checked = gcalEnabled;
    if (gcalToggleLbl) gcalToggleLbl.textContent = gcalEnabled ? 'Auto-sync on' : 'Auto-sync off';
    gcalBar.classList.toggle('gsync--disconnected', !gcalConnected);
    if (gcalSyncBtn) gcalSyncBtn.hidden = !active;
    if (gcalDiscBtn) gcalDiscBtn.hidden = !gcalConnected;

    gcalBar.classList.toggle('gsync--notice', !!gcalNotice);
    let text = gcalFlash || gcalNotice;
    if (!text && !gcalConnected) {
      text = 'Sign in once and every booking you mark here lands on your calendar.';
    } else if (!text && !gcalEnabled) {
      const who = g.account ? `${g.account} · ` : '';
      text = `${who}Auto-sync is off. New bookings and changes stay out of your calendar until you turn it back on.`;
    } else if (!text) {
      const who = g.account ? `${g.account} · ` : '';
      const n = Number(g.events) || 0;
      text = `${who}${n} booking${n === 1 ? '' : 's'} synced · checked ${agoText(g.last_sync)}`;
      if (g.last_error) { console.warn('gcal sync problem', g.last_error); text += " · the last sync didn't finish; it retries on its own"; }
    }
    if (gcalText) gcalText.textContent = text;
    // The cards' pill/link state depends on whether the sync is running;
    // redraw when that flips (connect, disconnect, pause, resume).
    if (wasActive !== active && lastJobs) {
      lastRenderKey = null;   // the render key ignores sync state; force the rebuild
      renderQueue(lastJobs);
    }
  }

  // CAL-05: pause / resume. The server answers with the fresh status, so
  // the panel and the cards update at once; the next poll confirms.
  if (gcalToggle) {
    gcalToggle.addEventListener('change', async () => {
      const enabled = gcalToggle.checked;
      gcalToggle.disabled = true;
      if (gcalToggleLbl) gcalToggleLbl.textContent = enabled ? 'Turning on…' : 'Turning off…';
      let ok = false;
      try {
        const r = await fetch(`${API_BASE}/gcal/enable`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ enabled }),
        });
        if (r.ok) { ok = true; gcalToggle.disabled = false; renderGcal(await r.json()); }
      } catch (_) { /* handled below */ }
      if (!ok) {
        // Say so instead of letting the next poll silently flip the switch back.
        gcalToggle.disabled = false;
        gcalToggle.checked = !enabled;
        gcalNotice = `Couldn't turn auto-sync ${enabled ? 'on' : 'off'}. Check your connection and try again.`;
        setTimeout(() => { gcalNotice = ''; fetchDiagnostic(); }, 6000);
      }
      fetchDiagnostic();
    });
  }
  if (gcalBannerX && gcalBanner) {
    gcalBannerX.addEventListener('click', () => {
      try { localStorage.setItem(GCAL_BANNER_SNOOZE_KEY, String(Date.now() + 7 * 86400 * 1000)); } catch (_) { /* private mode */ }
      gcalBanner.hidden = true;
    });
  }

  if (gcalSyncBtn) {
    gcalSyncBtn.addEventListener('click', async () => {
      gcalSyncBtn.disabled = true;
      try {
        await fetch(`${API_BASE}/gcal/sync`, { method: 'POST' });
      } catch (_) { /* next diagnostic poll reconciles */ }
      setTimeout(() => { gcalSyncBtn.disabled = false; fetchDiagnostic(); }, 2500);
    });
  }
  if (gcalDiscBtn) {
    gcalDiscBtn.addEventListener('click', async () => {
      if (!window.confirm('Disconnect Google Calendar? New bookings stop syncing. Events already on your calendar stay.')) return;
      try {
        await fetch(`${API_BASE}/gcal/disconnect`, { method: 'POST' });
      } catch (_) { /* next diagnostic poll reconciles */ }
      fetchDiagnostic();
    });
  }
  // After Google sends Marco back (/earlscheib?gcal=connected|error): land
  // on the Scheduled view, show the result once, clean the URL. Deferred so
  // wireFilters() has attached the chip handlers first.
  setTimeout(() => {
    const qs = new URLSearchParams(window.location.search);
    const r = qs.get('gcal');
    if (!r) return;
    gcalFlashKind = r === 'connected' ? 'ok' : 'error';
    gcalFlash = r === 'connected'
      ? 'Google Calendar connected. Your upcoming bookings are being added now.'
      : "Google sign-in didn't finish. Tap Continue with Google to try again.";
    if (gcalFlashKind === 'error') console.warn('gcal connect failed:', qs.get('why') || 'unknown');
    if (gcalFlashKind === 'error') { try { localStorage.removeItem(GCAL_BANNER_SNOOZE_KEY); } catch (_) { /* ignore */ } }
    setTimeout(() => { gcalFlash = ''; gcalFlashKind = ''; fetchDiagnostic(); }, 20000);
    const chip = document.querySelector('.filter[data-filter="scheduled"]');
    if (chip) chip.click();
    qs.delete('gcal');
    qs.delete('why');
    const clean = window.location.pathname + (qs.toString() ? `?${qs}` : '') + window.location.hash;
    window.history.replaceState(null, '', clean);
    fetchDiagnostic();
  }, 0);

  // CAL-02 (per Marco): tapping an appointment jumps to that customer's
  // card below and flashes it. A search that hides the card is cleared.
  function jumpToCard(key) {
    const sel = `.estimate-card[data-estimate-key="${CSS.escape(key)}"]`;
    let card = queueEl.querySelector(sel);
    if (!card && currentSearch && searchEl) {
      searchEl.value = '';
      currentSearch = '';
      renderQueue(lastJobs);
      card = queueEl.querySelector(sel);
    }
    if (!card) return;
    card.scrollIntoView({ behavior: 'smooth', block: 'start' });
    card.classList.add('estimate-card--flash');
    setTimeout(() => card.classList.remove('estimate-card--flash'), 1800);
  }

  // CAL-03 (per Marco): tapping an appointment opens a popup with the
  // customer's details and the card's own buttons (Change date, Add to
  // Google Calendar, Unmark, Cancel follow-ups) plus Call, Text and
  // History, instead of scrolling down to the card.
  const apptDrawer = document.getElementById('appt-drawer');
  const apptBody   = document.getElementById('appt-body');
  let apptKey = null;
  let apptSig = '';

  function apptGroup(key) {
    return groupByEstimate(lastJobs || []).find((g) => g.key === key) || null;
  }

  function apptSignature(g) {
    return [g.booked, g.appointment_at, g.jobs.map((j) => `${j.id}:${j.sent}:${j.cancelled}`).join(',')].join('|');
  }

  function openApptPopup(key) {
    const group = apptGroup(key);
    if (!group || !apptDrawer) { jumpToCard(key); return; }
    apptKey = key;
    apptSig = apptSignature(group);
    apptBody.innerHTML = '';
    const frag = buildEstimateCard(group, [], 0);
    const card = frag.querySelector('.estimate-card');
    card.classList.add('estimate-card--popup');
    const tl = frag.querySelector('.timeline');
    if (tl) tl.remove();
    const acts = document.createElement('div');
    acts.className = 'appt-actions';
    acts.innerHTML = (group.phone
      ? `<a class="btn btn-secondary" href="tel:${escapeHTML(group.phone)}">Call</a>`
        + `<a class="btn btn-secondary" href="/earlscheib/messages#${encodeURIComponent(group.phone)}">Text</a>`
      : '')
      + '<button class="btn btn-tertiary appt-history" type="button">History</button>';
    acts.querySelector('.appt-history').addEventListener('click', () => {
      closeApptPopup();
      window.openTimeline(key);
    });
    card.appendChild(acts);
    apptBody.appendChild(frag);
    apptDrawer.hidden = false;
    const t = document.getElementById('appt-title');
    if (t) requestAnimationFrame(() => t.focus({ preventScroll: true }));
  }

  function closeApptPopup() {
    if (apptDrawer) apptDrawer.hidden = true;
    apptKey = null;
  }

  // Keep an open popup current after Change date / Unmark / Cancel
  // follow-ups (each refetches the queue). Left alone while the date
  // picker is open so a background refresh never wipes a half-picked date.
  function refreshApptPopup() {
    if (!apptKey || !apptDrawer || apptDrawer.hidden) return;
    if (apptBody.querySelector('.book-form:not([hidden])')) return;
    const group = apptGroup(apptKey);
    if (!group) { closeApptPopup(); return; }
    if (apptSignature(group) !== apptSig) openApptPopup(apptKey);
  }

  if (apptDrawer) {
    apptDrawer.querySelectorAll('[data-close]').forEach((b) => b.addEventListener('click', closeApptPopup));
    document.addEventListener('keydown', (e) => { if (e.key === 'Escape') closeApptPopup(); });
  }

  if (calEl) {
    calGridEl.addEventListener('click', (e) => {
      const chip = e.target.closest('.cal-appt');
      if (chip && chip.dataset.key) openApptPopup(chip.dataset.key);
    });
    calEl.querySelector('.cal-prev').addEventListener('click', () => {
      calMonth = new Date(calMonth.getFullYear(), calMonth.getMonth() - 1, 1);
      renderCalendar();
    });
    calEl.querySelector('.cal-next').addEventListener('click', () => {
      calMonth = new Date(calMonth.getFullYear(), calMonth.getMonth() + 1, 1);
      renderCalendar();
    });
    calEl.querySelector('.cal-today').addEventListener('click', () => {
      const d = new Date();
      calMonth = new Date(d.getFullYear(), d.getMonth(), 1);
      renderCalendar();
    });
  }

  // ---------- Schedule flow (SCH-01) ----------------------------------

  // datetime-local wants "YYYY-MM-DDTHH:MM" in the *browser's* local zone
  // (Marco's browser runs Pacific, matching the scheduler's display zone).
  function toLocalInputValue(unixSeconds) {
    const d = new Date(unixSeconds * 1000);
    const pad = (n) => String(n).padStart(2, '0');
    return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
      + `T${pad(d.getHours())}:${pad(d.getMinutes())}`;
  }

  function toggleScheduleForm(job, formEl) {
    if (!formEl.hidden) { formEl.hidden = true; return; }
    const input = formEl.querySelector('.schedule-input');
    const nowSec = Math.floor(Date.now() / 1000);
    // Prefill with the row's current send time; if that's already past
    // (overdue row), suggest an hour from now instead.
    const seed = (job.send_at && job.send_at > nowSec)
      ? job.send_at
      : nowSec + 3600;
    input.value = toLocalInputValue(seed);
    input.min = toLocalInputValue(nowSec);
    formEl.hidden = false;
    input.focus();
  }

  async function handleSchedule(job, formEl, btnEl, errEl) {
    const input = formEl.querySelector('.schedule-input');
    if (!input.value) {
      showEntryError(errEl, 'Pick a date and time first.');
      return;
    }
    const ts = Math.floor(new Date(input.value).getTime() / 1000);
    if (!Number.isFinite(ts)) {
      showEntryError(errEl, "That date didn't read right. Try again.");
      return;
    }
    if (ts < Math.floor(Date.now() / 1000) - 60) {
      showEntryError(errEl, 'That time has already passed. Pick a later one.');
      return;
    }

    btnEl.disabled = true;
    errEl.hidden = true;
    errEl.textContent = '';

    try {
      const resp = await fetch(`${API_BASE}/queue/schedule`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ id: job.id, send_at: ts }),
      });
      if (resp.status === 200) {
        btnEl.textContent = 'Scheduled ✓';
        // Re-fetch so the row shows the new time + "Scheduled by you" tag
        // (row rebuild also collapses the form). The fallback reset covers
        // the no-op case (same time re-picked → render key unchanged).
        setTimeout(fetchQueue, 600);
        setTimeout(() => {
          formEl.hidden = true;
          btnEl.disabled = false;
          btnEl.textContent = 'Set time';
        }, 1400);
        return;
      }
      if (resp.status === 404) {
        showEntryError(errEl, 'This text was already sent or cancelled. Refreshing…');
        setTimeout(fetchQueue, 600);
        btnEl.disabled = false;
        return;
      }
      const parsed = await resp.json().catch(() => ({}));
      showEntryError(errEl, friendlyError('Scheduling', parsed, resp.status));
      btnEl.disabled = false;
    } catch (e) {
      showEntryError(errEl, NETWORK_ERROR);
      btnEl.disabled = false;
    }
  }

  // ---------- Resend flow (GLV-01) ------------------------------------

  // Resend an already-sent job. Does NOT flip the row's sent flag — the row
  // stays "sent" and the audit trail of repeated sends lives in sms_log.
  async function handleResend(job, entryEl, btnEl, errEl) {
    const type = (JOB_TYPE_LABELS[job.job_type] || job.job_type).toLowerCase();
    const confirmMsg = job.is_test === 1
      ? `Send the ${type} text to the test phones again? The original stays marked sent.`
      : `Send the ${type} text to ${job.name || 'this customer'} again? The original stays marked sent.`;
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
      showEntryError(errEl, friendlyError('The resend', parsed, resp.status));
      btnEl.disabled = false;
      if (btnEl.querySelector('span')) btnEl.querySelector('span').textContent = origLabel || 'Resend';
    } catch (e) {
      showEntryError(errEl, NETWORK_ERROR);
      btnEl.disabled = false;
      if (btnEl.querySelector('span')) btnEl.querySelector('span').textContent = origLabel || 'Resend';
    }
  }

  // ---------- Uncancel flow (GLV-04) ----------------------------------

  // Flip cancelled=1 back to cancelled=0 on a row so it reappears in
  // Estimates / Work Completed as pending. No SMS is sent — Marco can then
  // click Send-now (or wait for the scheduler when it's re-enabled).
  async function handleUncancel(job, entryEl, btnEl, errEl) {
    const type = (JOB_TYPE_LABELS[job.job_type] || job.job_type).toLowerCase();
    if (!window.confirm(`Restore the ${type} for ${job.name || 'this customer'}? It goes back to pending and sends on schedule.`)) return;

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
      showEntryError(errEl, friendlyError('Restoring', parsed, resp.status));
      btnEl.disabled = false;
    } catch (e) {
      showEntryError(errEl, NETWORK_ERROR);
      btnEl.disabled = false;
    }
  }

  function markEntrySent(entryEl) {
    const stamp = timeFmt.format(new Date());
    entryEl.dataset.state = 'sent';
    const stampEl = entryEl.querySelector('.timeline__sent-stamp');
    if (stampEl) {
      stampEl.hidden = false;
      stampEl.textContent = 'Sent Today ' + stamp;
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
    const type = (JOB_TYPE_LABELS[job.job_type] || job.job_type).toLowerCase();
    if (!window.confirm(`Cancel the ${type} for ${name}? You can restore it later from the Cancelled view.`)) return;

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
        counters.failed++;
        updateStats();
        entryEl.dataset.state = 'pending';
        btnEl.disabled = false;
        showEntryError(errEl, friendlyError('Cancelling', parsed, resp.status));
      }
    } catch (_) {
      counters.failed++;
      updateStats();
      entryEl.dataset.state = 'pending';
      btnEl.disabled = false;
      showEntryError(errEl, NETWORK_ERROR);
    }
  }

  // ---------- Data fetch + refresh ------------------------------------

  async function fetchQueue() {
    if (refreshBtn) refreshBtn.classList.add('spinning');
    try {
      const resp = await fetch(`${API_BASE}/queue?status=all`, { cache: 'no-store' });
      if (!resp.ok) {
        const parsed = await resp.json().catch(() => ({}));
        console.warn('queue fetch failed', resp.status, parsed.error || '');
        showError("The queue didn't load. It retries on its own every few seconds.");
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
    const retry = frag.querySelector('.error-retry');
    if (retry) retry.addEventListener('click', () => { retry.disabled = true; fetchQueue(); });
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
      syncCaptionEl.textContent = 'Updated just now';
    } else if (ago < 60) {
      syncCaptionEl.textContent = `Updated ${ago} s ago`;
    } else {
      const m = Math.floor(ago / 60);
      syncCaptionEl.textContent = `Updated ${m} min ago`;
    }
    // Dot: green while polls land on time, amber once the data is stale.
    if (syncDotEl) {
      syncDotEl.classList.toggle('is-stale', ago >= REFRESH_MS / 1000 * 2 || inRetryMode);
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

      // WNC-01: auto-send toggle. Visible only when /diagnostic returns a
      // boolean scheduler_enabled (Pi-served path). Hidden in local Go admin
      // mode (returns undefined) — we never POST to /api/auto-send.
      // Guard: typeof check so pre-SPN UI or Go admin gracefully hides toggle.
      const toggleBar = document.getElementById('auto-send-toggle');
      const toggleCheckbox = document.getElementById('auto-send-checkbox');
      const toggleLabel = document.getElementById('auto-send-label');
      if (toggleBar && typeof d.scheduler_enabled === 'boolean') {
        toggleBar.hidden = false;
        // Programmatic state sync from poll — do NOT trigger the change handler.
        if (toggleCheckbox) {
          toggleCheckbox.dataset.syncing = '1';
          toggleCheckbox.checked = d.scheduler_enabled;
          delete toggleCheckbox.dataset.syncing;
        }
        if (toggleLabel) {
          toggleLabel.textContent = d.scheduler_enabled
            ? 'Auto-send ON — texts go out automatically'
            : 'Auto-send OFF — paused; manual Send now still works';
        }
      } else if (toggleBar) {
        toggleBar.hidden = true;
      }

      // CAL-04: Google Calendar sync line on the Scheduled view.
      renderGcal(d.gcal);
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
    // INS-01: the test-mode chips (and their separator + re-seed button) are
    // dev tooling. Marco never needs them, so they are hidden unless the URL
    // carries ?test — e.g. /earlscheib?test=1#queue.
    if (!new URLSearchParams(window.location.search).has('test')) {
      document.querySelectorAll('[data-test-chip]').forEach((c) => { c.hidden = true; });
    }

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

  // ---------- Empty / error blocks for Templates, Schedules, Logs -------
  // Same shape as the queue's states: icon well, title, one sentence, and
  // (for errors) a Try again button. Never shows codes or tags to Marco.
  const STATE_ICONS = {
    inbox: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M22 12h-6l-2 3h-4l-2-3H2"/><path d="M5.45 5.11 2 12v6a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-6l-3.45-6.89A2 2 0 0 0 16.76 4H7.24a2 2 0 0 0-1.79 1.11z"/></svg>',
    alert: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="9"/><path d="M12 8v4M12 16h.01"/></svg>',
    chat:  '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M21 11.5a8.38 8.38 0 0 1-.9 3.8 8.5 8.5 0 0 1-7.6 4.7 8.38 8.38 0 0 1-3.8-.9L3 21l1.9-5.7a8.38 8.38 0 0 1-.9-3.8 8.5 8.5 0 0 1 4.7-7.6 8.38 8.38 0 0 1 3.8-.9h.5a8.48 8.48 0 0 1 8 8v.5z"/></svg>',
  };
  function stateBlock(opts) {
    const el = document.createElement('div');
    el.className = 'state' + (opts.error ? ' state--error' : '') + (opts.inline ? ' state--inline' : '');
    const icon = document.createElement('span');
    icon.className = 'state__icon' + (opts.error ? ' state__icon--error' : '');
    icon.setAttribute('aria-hidden', 'true');
    icon.innerHTML = STATE_ICONS[opts.icon || (opts.error ? 'alert' : 'inbox')];
    el.appendChild(icon);
    const title = document.createElement('h3');
    title.className = 'state__title';
    title.textContent = opts.title;
    el.appendChild(title);
    if (opts.text) {
      const text = document.createElement('p');
      text.className = 'state__text';
      text.textContent = opts.text;
      el.appendChild(text);
    }
    if (opts.action && opts.onAction) {
      const btn = document.createElement('button');
      btn.type = 'button';
      btn.className = 'btn btn-secondary state__action';
      btn.textContent = opts.action;
      btn.addEventListener('click', () => { btn.disabled = true; opts.onAction(); });
      el.appendChild(btn);
    }
    return el;
  }

  // Plain-English tooltips for the Templates variable chips (spec 4.4).
  const PLACEHOLDER_HELP = {
    first_name:   "Customer's first name",
    name:         "Customer's full name",
    phone:        "Customer's phone number",
    vin:          'Vehicle VIN',
    year:         'Vehicle year',
    make:         'Vehicle make (e.g. Honda)',
    model:        'Vehicle model (e.g. Civic LX)',
    short_model:  'Short model name (e.g. Civic)',
    vehicle_desc: 'Full vehicle description',
    ro_id:        'Repair order number',
    doc_id:       'CCC estimate ID',
    email:        "Customer's email",
    appt_time:    'Booked drop-off date and time',
    shop_name:    'Shop name',
    shop_phone:   'Shop phone number',
    review_url:   'Google review link',
  };

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
        console.warn('templates fetch failed', resp.status);
        listEl.innerHTML = '';
        listEl.appendChild(stateBlock({ error: true, title: "Couldn't load the templates",
          text: 'The server answered, but not with the templates.', action: 'Try again', onAction: () => loadTemplates(true) }));
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
      listEl.appendChild(stateBlock({ error: true, title: "Can't reach the server",
        text: 'Check your connection, then try again.', action: 'Try again', onAction: () => loadTemplates(true) }));
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
      chip.className = 'tpl-chip mono';
      chip.textContent = `{${name}}`;
      chip.setAttribute('data-var', name);
      chip.title = PLACEHOLDER_HELP[name] || name.replace(/_/g, ' ');
      chip.setAttribute('aria-label', `Insert ${chip.title}`);
      chipsEl.appendChild(chip);
    });

    // "Preview · sent to Carla's 2021 Civic" when a real pending job exists.
    const previewCap = frag.querySelector('.tpl-card__preview-label');
    if (previewCap) {
      const smp = templateState.sample || {};
      const who = smp.first_name || (smp.name ? String(smp.name).split(/\s+/)[0] : '');
      const car = [smp.year, smp.short_model || smp.model].filter(Boolean).join(' ');
      previewCap.textContent = who ? `Preview · sent to ${who}'s ${car || 'vehicle'}` : 'Preview';
    }

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
    const counterWrap = frag.querySelector('.tpl-card__counter');
    const paintCounter = (n) => {
      counter.textContent = String(n);
      if (counterWrap) {
        counterWrap.classList.toggle('is-warn', n >= 1500 && n < 2000);
        counterWrap.classList.toggle('is-max', n >= 2000);
      }
    };
    paintCounter(textarea.value.length);
    previewEl.textContent = renderTemplate(textarea.value, templateState.sample);
    resetBtn.disabled = !jt.is_override;
    // Auto-grow the body with its content (min 4 rows via CSS min-height).
    const autoGrow = () => { textarea.style.height = 'auto'; textarea.style.height = `${textarea.scrollHeight + 2}px`; };
    requestAnimationFrame(autoGrow);

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
      paintCounter(textarea.value.length);
      autoGrow();
      if (previewTimer) clearTimeout(previewTimer);
      previewTimer = setTimeout(() => {
        previewEl.textContent = renderTemplate(textarea.value, templateState.sample);
      }, SEARCH_DEBOUNCE_MS);

      const saved = templateState.cards[jt.job_type] || { body: '' };
      const isDirty = textarea.value !== saved.body;
      dirtyDot.hidden = !isDirty;
      article.classList.toggle('is-dirty', isDirty);
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
        showStatus(statusEl, 'A { is missing its closing }. Fix the braces and save again.', 'error');
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
          console.warn('template save failed', resp.status, parsed);
          const msg = parsed.detail || friendlyError('Saving', parsed, resp.status);
          showStatus(statusEl, msg, 'error');
          // Re-enable so user can retry.
          saveBtn.disabled = false;
          resetBtn.disabled = !(templateState.cards[jt.job_type] || {}).is_override;
          return;
        }
        applySavedTemplate(jt.job_type, parsed, textarea, badge, dirtyDot,
                           saveBtn, resetBtn, statusEl, previewEl);
      } catch (_) {
        showStatus(statusEl, NETWORK_ERROR, 'error');
        saveBtn.disabled = false;
        resetBtn.disabled = !(templateState.cards[jt.job_type] || {}).is_override;
      }
    });

    // Reset to default — PUT with empty body (server DELETEs the row).
    resetBtn.addEventListener('click', async () => {
      if (!window.confirm('Put the default text back for this follow-up? Your custom version is removed.')) return;
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
          showStatus(statusEl, friendlyError('Resetting', parsed, resp.status), 'error');
          saveBtn.disabled = false;
          resetBtn.disabled = false;
          return;
        }
        applySavedTemplate(jt.job_type, parsed, textarea, badge, dirtyDot,
                           saveBtn, resetBtn, statusEl, previewEl);
      } catch (_) {
        showStatus(statusEl, NETWORK_ERROR, 'error');
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
    const card = textarea.closest('.tpl-card');
    if (card) card.classList.remove('is-dirty');
    const tCounter = card ? card.querySelector('.tpl-card__counter') : null;
    if (tCounter) { tCounter.classList.toggle('is-warn', newBody.length >= 1500 && newBody.length < 2000); tCounter.classList.toggle('is-max', newBody.length >= 2000); }
    textarea.style.height = 'auto'; textarea.style.height = `${textarea.scrollHeight + 2}px`;
    saveBtn.disabled = true;
    resetBtn.disabled = !parsed.is_override;

    const msg = parsed.is_override ? 'Saved' : 'Default text restored';
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
        console.warn('schedules fetch failed', resp.status);
        listEl.innerHTML = '';
        listEl.appendChild(stateBlock({ error: true, title: "Couldn't load the schedules",
          text: 'The server answered, but not with the schedules.', action: 'Try again', onAction: () => loadSchedules(true) }));
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
      listEl.appendChild(stateBlock({ error: true, title: "Can't reach the server",
        text: 'Check your connection, then try again.', action: 'Try again', onAction: () => loadSchedules(true) }));
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
    toggleLabel.textContent = enabled ? 'On' : 'Off';
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
      article.classList.toggle('is-dirty', isDirty);
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
          `Pick a delay between ${scheduleState.bounds.min} and ${scheduleState.bounds.max} hours.`,
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
          showStatus(statusEl, friendlyError('Saving', parsed, resp.status), 'error');
          saveBtn.disabled = false;
          resetBtn.disabled = !(scheduleState.cards[jt.job_type] || {}).is_override;
          return;
        }
        applySavedSchedule(jt.job_type, parsed, input, daysEl, badge,
                           dirtyDot, saveBtn, resetBtn, statusEl);
      } catch (_) {
        showStatus(statusEl, NETWORK_ERROR, 'error');
        saveBtn.disabled = false;
        resetBtn.disabled = !(scheduleState.cards[jt.job_type] || {}).is_override;
      }
    });

    // Reset to default: PUT {} so server reverts.
    resetBtn.addEventListener('click', async () => {
      if (!window.confirm('Put the default delay back for this follow-up? Pending texts move to the default timing.')) return;
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
          showStatus(statusEl, friendlyError('Resetting', parsed, resp.status), 'error');
          saveBtn.disabled = false;
          resetBtn.disabled = false;
          return;
        }
        // Reset reverts both fields server-side; the toggle visual must follow.
        const newEnabled = parsed.enabled !== false;
        toggleInput.checked = newEnabled;
        toggleLabel.textContent = newEnabled ? 'On' : 'Off';
        article.classList.toggle('is-disabled', !newEnabled);
        applySavedSchedule(jt.job_type, parsed, input, daysEl, badge,
                           dirtyDot, saveBtn, resetBtn, statusEl);
      } catch (_) {
        showStatus(statusEl, NETWORK_ERROR, 'error');
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
          toggleLabel.textContent = !next ? 'On' : 'Off';
          article.classList.toggle('is-disabled', next);
          showStatus(statusEl, friendlyError(next ? 'Turning on' : 'Turning off', parsed, resp.status), 'error');
          return;
        }
        // Reflect server state.
        const newEnabled = parsed.enabled !== false;
        toggleInput.checked = newEnabled;
        toggleLabel.textContent = newEnabled ? 'On' : 'Off';
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
          msg = 'Turned on. New texts of this kind go out on schedule.';
        } else {
          msg = cancelled > 0
            ? `Turned off. ${cancelled} pending text${cancelled === 1 ? '' : 's'} cancelled.`
            : 'Turned off. No new texts of this kind go out.';
        }
        showStatus(statusEl, msg, 'ok');
      } catch (_) {
        toggleInput.checked = !next;
        toggleLabel.textContent = !next ? 'On' : 'Off';
        article.classList.toggle('is-disabled', next);
        showStatus(statusEl, NETWORK_ERROR, 'error');
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
    const card = input.closest('.sched-card');
    if (card) card.classList.remove('is-dirty');
    saveBtn.disabled = true;
    resetBtn.disabled = !parsed.is_override;

    const rebased = typeof parsed.rebased_jobs === 'number' ? parsed.rebased_jobs : 0;
    const moved = rebased > 0 ? ` ${rebased} pending text${rebased === 1 ? '' : 's'} moved to the new timing.` : '';
    const msg = parsed.is_override ? `Saved.${moved}` : `Default delay restored.${moved}`;
    showStatus(statusEl, msg, 'ok');
  }

  function wireTopnav() {
    const links = document.querySelectorAll('.topnav-link');
    const viewQueue = document.getElementById('view-queue');
    const viewTpl   = document.getElementById('view-templates');
    const viewSched = document.getElementById('view-schedules');
    const viewLogs  = document.getElementById('view-logs');
    const viewIns   = document.getElementById('view-insights');
    if (!links.length || !viewQueue || !viewTpl) return;

    const TITLES = { queue: 'Queue', templates: 'Templates', schedules: 'Schedules', logs: 'Logs', insights: 'Insights' };
    const activate = (target) => {
      document.title = `Earl Scheib Concord — ${TITLES[target] || 'Follow-ups'}`;
      links.forEach((a) => {
        const on = a.getAttribute('data-view') === target;
        a.classList.toggle('is-active', on);
        a.setAttribute('aria-selected', on ? 'true' : 'false');
        if (on) a.setAttribute('aria-current', 'page'); else a.removeAttribute('aria-current');
      });
      viewQueue.hidden = target !== 'queue';
      viewTpl.hidden   = target !== 'templates';
      if (viewSched) viewSched.hidden = target !== 'schedules';
      if (viewLogs)  viewLogs.hidden  = target !== 'logs';
      if (viewIns)   viewIns.hidden   = target !== 'insights';
      if (target === 'templates') loadTemplates(false);
      if (target === 'schedules') loadSchedules(false);
      if (target === 'logs')      loadTwilioMessages();
      if (target === 'insights')  loadInsights();
    };

    links.forEach((a) => {
      a.addEventListener('click', (ev) => {
        // data-external links navigate normally (e.g. the two-way inbox at
        // /earlscheib/messages) instead of switching an in-page view.
        if (a.hasAttribute('data-external')) return;
        ev.preventDefault();
        const target = a.getAttribute('data-view') || 'queue';
        activate(target);
      });
    });

    // Allow direct-link via hash (e.g. refreshes on /earlscheib#templates).
    // The Queue link is /earlscheib#queue and #queue is the cards container:
    // the browser's fragment scroll would land past the chips and search, so
    // put the view back at the top (both scroll containers: window on
    // desktop, the view itself on the mobile app-shell).
    if (window.location.hash === '#queue') {
      const toTop = () => { window.scrollTo(0, 0); viewQueue.scrollTop = 0; };
      toTop();
      window.addEventListener('load', () => requestAnimationFrame(toTop), { once: true });
    }
    if (window.location.hash === '#templates') activate('templates');
    if (window.location.hash === '#schedules') activate('schedules');
    if (window.location.hash === '#logs')      activate('logs');
    if (window.location.hash === '#insights')  activate('insights');
  }

  // ---------- Logs view (USH-01) --------------------------------------
  // Twilio-backed message log. Replaces the previous sms_log-backed view —
  // the local table couldn't distinguish "we tried to send to the customer"
  // from "we actually sent to the customer" when TEST_PHONE_RECIPIENTS
  // fan-out was toggled. Twilio's Messages API is the source of truth.

  const currentLogsFilters = { status: 'all', direction: 'all', days: 30 };

  // GET endpoint — no auth body, query-string only. CF Access gates the edge.
  const twilioMessagesURL = IS_LOCAL_ADMIN
    ? '/api/twilio-messages'
    : `${API_BASE}/twilio-messages`;

  async function loadTwilioMessages() {
    const listEl = document.getElementById('logs-list');
    if (!listEl) return;
    const params = new URLSearchParams({
      status: currentLogsFilters.status,
      direction: currentLogsFilters.direction,
      days: String(currentLogsFilters.days),
      limit: '200',
    });
    try {
      const resp = await fetch(`${twilioMessagesURL}?${params.toString()}`, {
        method: 'GET',
        headers: { 'Accept': 'application/json' },
      });
      if (!resp.ok) {
        // The tag (e.g. twilio_creds_missing) goes to the console, not to Marco.
        let detail = `HTTP ${resp.status}`;
        try {
          const data = await resp.json();
          if (data && data.error) detail = `${detail} (${data.error})`;
        } catch (_) { /* body wasn't JSON */ }
        console.warn('message log fetch failed', detail);
        listEl.innerHTML = '';
        listEl.appendChild(stateBlock({ error: true, title: "Couldn't load the log from Twilio",
          text: 'Retrying in 15 seconds.', action: 'Try again', onAction: loadTwilioMessages }));
        updateLogsFreshness(null);
        return;
      }
      const data = await resp.json();
      renderTwilioMessages(data.rows || []);
      updateLogsFreshness(data);
    } catch (exc) {
      console.warn('message log fetch failed', exc);
      listEl.innerHTML = '';
      listEl.appendChild(stateBlock({ error: true, title: "Can't reach the server",
        text: 'Retrying in 15 seconds.', action: 'Try again', onAction: loadTwilioMessages }));
      updateLogsFreshness(null);
    }
  }

  function renderTwilioMessages(rows) {
    const listEl = document.getElementById('logs-list');
    if (!listEl) return;
    if (!rows.length) {
      listEl.innerHTML = '';
      listEl.appendChild(stateBlock({ icon: 'chat', title: 'No messages in this range',
        text: 'Nothing went in or out for these filters. Try a wider range.' }));
      return;
    }
    const ARROW_OUT = '<svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M4 12L12 4M6 4h6v6"/></svg>';
    const ARROW_IN  = '<svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M12 4L4 12M10 12H4V6"/></svg>';
    const STATUS_LABEL = { delivered: 'Delivered', sent: 'Sent', failed: 'Failed', undelivered: 'Undelivered',
      received: 'Received', queued: 'Queued', sending: 'Sending', accepted: 'Accepted', receiving: 'Receiving' };
    const table = document.createElement('div');
    table.className = 'logs-table';
    table.setAttribute('role', 'table');
    table.innerHTML = '<div class="logs-table__head" role="row" aria-hidden="true">'
      + '<span>When</span><span></span><span>Customer</span><span>Message</span><span style="text-align:right">Status</span></div>';
    rows.forEach((row) => {
      const div = document.createElement('div');
      div.className = 'log-row';
      div.setAttribute('role', 'row');
      const direction = row.direction === 'inbound' ? 'inbound' : 'outbound';
      div.dataset.direction = direction;
      const tsStr = row.date_sent ? formatWhen(row.date_sent) : '—';
      const status = (row.status || '').toLowerCase();
      const statusLabel = STATUS_LABEL[status] || (status ? status.charAt(0).toUpperCase() + status.slice(1) : '—');
      const customer = row.customer_name || (direction === 'inbound' ? 'Unknown number' : 'Customer');
      const matchPhone = direction === 'inbound' ? (row.from || '') : (row.to || '');
      const phone = matchPhone ? formatPhone(matchPhone) : '';
      const jobType = row.job_type && JOB_TYPE_LABELS[row.job_type] ? JOB_TYPE_LABELS[row.job_type] : (row.job_type && row.job_type !== '—' ? row.job_type : '');
      const body = row.body || '';
      // Carrier failures: the message, never the numeric code (that stays in the title).
      let err = '';
      if (row.error_message || row.error_code) {
        const txt = row.error_message || 'The carrier could not deliver this text.';
        err = `<small title="${escapeAttr(row.error_code ? `Twilio error ${row.error_code}` : '')}">${escapeHTML(txt)}</small>`;
      }
      div.innerHTML = `
        <div class="log-row__ts">${escapeHTML(tsStr)}</div>
        <div class="log-row__direction" data-direction="${escapeAttr(direction)}" title="${direction === 'inbound' ? 'From the customer' : 'From the shop'}">${direction === 'inbound' ? ARROW_IN : ARROW_OUT}</div>
        <div class="log-row__customer">${escapeHTML(customer)}<span class="log-row__phone mono">${escapeHTML(phone)}</span><span class="log-row__job-type">${escapeHTML(jobType)}</span></div>
        <div class="log-row__body">${escapeHTML(body)}${err}</div>
        <div class="log-row__status pill" data-status="${escapeAttr(status)}">${escapeHTML(statusLabel)}</div>
        <div class="log-row__sid" title="${escapeAttr(row.sid || '')}" hidden>${escapeHTML(row.sid ? row.sid.slice(0, 8) : '')}</div>
      `;
      table.appendChild(div);
    });
    listEl.innerHTML = '';
    listEl.appendChild(table);
  }

  function updateLogsFreshness(data) {
    const el = document.getElementById('logs-freshness');
    if (!el) return;
    if (!data || !data.cached_at) {
      el.textContent = '';
      return;
    }
    const stale = Number(data.stale_seconds || 0);
    const count = Number(data.count || 0);
    const noun = count === 1 ? 'message' : 'messages';
    if (stale <= 1) {
      el.textContent = `${count} ${noun} · checked just now`;
    } else if (stale < 60) {
      el.textContent = `${count} ${noun} · checked ${stale} s ago`;
    } else {
      el.textContent = `${count} ${noun} · checked ${Math.floor(stale / 60)} min ago`;
    }
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
        const key = btn.getAttribute('data-filter-key');
        const value = btn.getAttribute('data-filter-value');
        if (!key || value === null) return;
        const coerced = key === 'days' ? Number(value) : value;
        if (currentLogsFilters[key] === coerced) return;
        currentLogsFilters[key] = coerced;
        // Update aria-selected only within this filter group.
        document.querySelectorAll(`.logs-filter[data-filter-key="${key}"]`).forEach((b) => {
          b.setAttribute(
            'aria-selected',
            b.getAttribute('data-filter-value') === value ? 'true' : 'false',
          );
        });
        loadTwilioMessages();
      });
    });
  }

  // ---------- Insights view (INS-01) --------------------------------
  // Marco-facing board: warm leads / this week / no-shows on top, period
  // numbers underneath. All of it comes from one GET /insights?days= call.
  let insDays = '30';
  // esc(): HTML-escape for the template literals below. escapeHTML() above is
  // declared twice in this file (queue + logs sections) so it is deliberately
  // not reused here — this one is null-safe and single-sourced.
  const esc = (s) => String(s == null ? '' : s).replace(/[&<>"']/g, (c) => (
    { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]
  ));
  // Copied from messages.html (the two pages do not share JS).
  const fmtPhone = (e164) => {
    const m = String(e164 || '').match(/^\+1(\d{3})(\d{3})(\d{4})$/);
    return m ? `(${m[1]}) ${m[2]}-${m[3]}` : (e164 || '');
  };
  const money = (n) => '$' + Math.round(n).toLocaleString('en-US');
  // ts === 0 means "this sync has never run" (e.g. no Twilio creds yet) —
  // without the guard that renders as "20700 days ago".
  const rel = (ts) => { if (!ts) return 'never'; const d = Math.floor((Date.now() / 1000 - ts) / 86400); return d <= 0 ? 'today' : d === 1 ? '1 day ago' : d + ' days ago'; };
  const when = (ts) => new Date(ts * 1000).toLocaleString('en-US', { weekday: 'short', month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' });
  const nameBtn = (row) => `<button class="ins-name" type="button" data-timeline-key="${esc(row.key)}">${esc(row.name || fmtPhone(row.phone))}</button>`;
  // Phone under the name as quiet mono meta (the insights payload carries no vehicle).
  const phoneMeta = (row) => (row.name && row.phone ? `<span class="mono">${esc(fmtPhone(row.phone))}</span>` : '');
  const CHEV = '<span class="ins-chev" aria-hidden="true"><svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M6 3l5 5-5 5"/></svg></span>';

  async function loadInsights(refresh) {
    const root = document.getElementById('view-insights');
    if (!root) return;
    // /insights/sync answers 202 the moment the background sync starts, so the
    // response body says nothing about the data. Give it a beat to land before
    // reading — whatever misses this window shows up on the next load.
    if (refresh) {
      try {
        await fetch(`${API_BASE}/insights/sync`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{}' });
        await new Promise((r) => setTimeout(r, 1500));
      } catch (_) {}
    }
    let data;
    try {
      const resp = await fetch(`${API_BASE}/insights?days=${insDays}`);
      if (!resp.ok) throw new Error(resp.status);
      data = await resp.json();
    } catch (err) {
      console.warn('insights fetch failed', err);
      root.querySelector('#ins-foot').textContent = "Couldn't load the numbers. Tap Refresh to try again.";
      return;
    }
    renderInsights(data);
  }

  function renderInsights(d) {
    const list = (id, rows, fmt, empty) => {
      const el = document.querySelector(`#${id} .ins-list`);
      el.innerHTML = rows.length ? rows.map(fmt).join('') : `<div class="ins-empty">${empty}</div>`;
    };
    list('ins-warm', d.warm_leads, (w) => `<div class="ins-row"><div style="min-width:0">${nameBtn(w)}<div class="ins-sub">${phoneMeta(w)}${phoneMeta(w) ? ' · ' : ''}${esc(w.last_reply)}</div></div><div class="ins-meta">${rel(w.last_reply_at)}${w.estimate_total ? '<br>' + money(w.estimate_total) : ''}</div><a class="btn btn-sm" href="/earlscheib/messages#${encodeURIComponent(w.phone)}">Reply</a></div>`, 'No open replies right now. Replies that have not turned into a booking show up here.');
    list('ins-upcoming', d.bookings_upcoming, (b) => `<div class="ins-row"><div style="min-width:0">${nameBtn(b)}<div class="ins-sub">${phoneMeta(b)}</div></div><div class="ins-meta">${esc(formatWhen(b.appointment_at))}${b.estimate_total ? '<br>' + money(b.estimate_total) : ''}</div>${CHEV}</div>`, 'Nothing booked for this week yet.');
    const ns = document.getElementById('ins-noshows'); ns.hidden = d.no_shows.length === 0;
    list('ins-noshows', d.no_shows, (n) => `<div class="ins-row"><div style="min-width:0">${nameBtn(n)}<div class="ins-sub">Was due ${esc(formatWhen(n.appointment_at))}</div></div><div class="ins-meta">${n.days_overdue} ${n.days_overdue === 1 ? 'day' : 'days'} ago</div><a class="btn btn-sm" href="/earlscheib/messages#${encodeURIComponent(n.phone)}">Text</a></div>`, '');
    const t = d.tiles;
    const tile = (label, v, prev, fmt) => { const delta = prev ? Math.round((v - prev) / prev * 100) : null; return `<div class="ins-tile"><div class="ins-v">${fmt(v)}</div><div class="ins-l">${label}</div>${delta === null ? '' : `<div class="ins-d ${delta >= 0 ? 'up' : 'down'}">${delta >= 0 ? '▲' : '▼'} ${Math.abs(delta)}% vs prior</div>`}</div>`; };
    document.querySelector('#ins-tiles .ins-tiles').innerHTML = [
      tile('estimates', t.estimates.value, t.estimates.prev, String), tile('texted', t.texted.value, t.texted.prev, String),
      tile('replies', t.replies.value, t.replies.prev, String), tile('bookings', t.bookings.value, t.bookings.prev, String),
      tile('closed ROs', t.closed_ros.value, t.closed_ros.prev, String), tile('revenue per CCC', t.revenue.value, t.revenue.prev, money),
      tile('avg ticket', t.avg_ticket.value, t.avg_ticket.prev, money), tile('your median reply', t.marco_reply_median_min.value, t.marco_reply_median_min.prev, (m) => m ? (m < 90 ? Math.round(m) + ' min' : (m / 60).toFixed(1) + ' h') : '—'),
    ].join('');
    const max = Math.max(1, d.funnel[0].n);
    document.querySelector('#ins-funnel .ins-bars').innerHTML = d.funnel.map((f) => `<div class="ins-bar"><span class="ins-bar-l">${esc(f.label)}</span><span class="ins-bar-t"><span class="ins-bar-f" style="width:${(f.n / max * 100).toFixed(1)}%"></span></span><span class="ins-bar-v">${f.n}${f.revenue ? ' · ' + money(f.revenue) : ''}</span></div>`).join('');
    const tp = d.template_reply_rate;
    document.querySelector('#ins-funnel .ins-tpl').innerHTML = ['24h', '3day', 'review'].map((k) => `<span>${k === '24h' ? '24-hour follow-up' : k === '3day' ? '3-day check-in' : 'Review request'}: ${tp[k].replied} of ${tp[k].sent} replied</span>`).join(' · ');
    const mmax = Math.max(1, ...d.revenue_by_month.map((m) => m.revenue));
    const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
    const colsEl = document.querySelector('#ins-revenue .ins-cols');
    colsEl.classList.toggle('is-empty', !d.revenue_by_month.length);
    if (d.revenue_by_month.length) {
      colsEl.innerHTML = d.revenue_by_month.map((m) => `<div class="ins-col"><div class="ins-col-v">${money(m.revenue)}</div><div class="ins-col-b" style="height:${(m.revenue / mmax * 100).toFixed(0)}%"></div><div class="ins-col-l">${MONTHS[Number(m.month.slice(5, 7)) - 1] || m.month.slice(5)} ${m.month.slice(2, 4)} · ${m.closed_ros} RO${m.closed_ros === 1 ? '' : 's'}</div></div>`).join('');
    } else {
      colsEl.innerHTML = '';
      colsEl.appendChild(stateBlock({ inline: true, title: 'No closed repair orders yet', text: 'Revenue appears here once a repair order closes in CCC.' }));
    }
    // Cents matter here and nowhere else: the whole period's texting bill is a
    // couple of dollars, so money()'s rounding would print "$1" for $0.83.
    document.getElementById('ins-foot').textContent = `Twilio cost $${Number(d.twilio_cost).toFixed(2)} this period · ${d.placeholder_estimates.n} of ${d.placeholder_estimates.of} open estimates still at the $472.50 placeholder`;
    const s = d.synced_at; document.getElementById('ins-synced').textContent = `CCC exports synced ${rel(s.ro_exports)} · texts synced ${rel(s.inbound_sms)}`;
    document.querySelectorAll('[data-timeline-key]').forEach((b) => b.addEventListener('click', () => window.openTimeline && window.openTimeline(b.dataset.timelineKey)));
  }

  document.querySelectorAll('#view-insights .chip[data-days]').forEach((c) => c.addEventListener('click', () => {
    document.querySelectorAll('#view-insights .chip').forEach((x) => x.classList.toggle('is-active', x === c));
    insDays = c.dataset.days; loadInsights();
  }));
  const insRefresh = document.getElementById('ins-refresh'); if (insRefresh) insRefresh.addEventListener('click', () => loadInsights(true));

  // ---------- Customer timeline drawer (INS-01) ------------------------
  // One customer's whole history in order: estimate → texts → replies →
  // booking → closed RO. Opened from any [data-timeline-key] button.
  // Duplicated (deliberately) inside messages.html — the two pages share no JS.
  window.openTimeline = async function (key) {
    const dr = document.getElementById('tl-drawer'); if (!dr) return;
    dr.hidden = false; document.getElementById('tl-list').innerHTML = '<li class="tl-empty">Loading…</li>';
    const nameEl = document.getElementById('tl-name');
    if (nameEl) requestAnimationFrame(() => nameEl.focus({ preventScroll: true }));
    try {
      const resp = await fetch(`${API_BASE}/customer?key=${encodeURIComponent(key)}`);
      if (!resp.ok) throw new Error(resp.status);
      const d = await resp.json();
      document.getElementById('tl-name').textContent = d.header.name || d.header.phone;
      document.getElementById('tl-sub').textContent = [d.header.vehicle, d.header.estimate_total ? '$' + Math.round(d.header.estimate_total).toLocaleString('en-US') + ' est.' : '', d.header.status].filter(Boolean).join(' · ');
      const ic = (d) => `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${d}</svg>`;
      const icon = {
        estimate:  ic('<path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><path d="M14 2v6h6M8 13h8M8 17h6"/>'),
        text:      ic('<path d="M4 12L12 4M6 4h6v6"/>'),
        reply:     ic('<path d="M12 4L4 12M10 12H4V6"/>'),
        booking:   ic('<rect x="3" y="5" width="18" height="16" rx="3"/><path d="M3 10h18M8 3v4M16 3v4"/>'),
        ro_closed: ic('<path d="M5 12l4 4L19 6"/>'),
      };
      document.getElementById('tl-list').innerHTML = d.events.map((e) => `<li class="tl-ev tl-${e.kind}"><span class="tl-ic">${icon[e.kind] || ic('<circle cx="12" cy="12" r="3"/>')}</span><div><div class="tl-l">${esc(e.label)}</div>${e.detail ? `<div class="tl-d">${esc(e.detail)}</div>` : ''}<div class="tl-t">${new Date(e.t * 1000).toLocaleString('en-US', { month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' })}</div></div></li>`).join('') || '<li class="tl-empty">No events.</li>';
    } catch (err) { console.warn('timeline fetch failed', err); document.getElementById('tl-list').innerHTML = "<li class=\"tl-empty\">Couldn't load this customer's history. Close and try again.</li>"; }
  };
  document.querySelectorAll('#tl-drawer [data-close]').forEach((b) => b.addEventListener('click', () => { document.getElementById('tl-drawer').hidden = true; }));
  document.addEventListener('keydown', (e) => { if (e.key === 'Escape') { const dr = document.getElementById('tl-drawer'); if (dr) dr.hidden = true; } });

  // Support/debug hook: /earlscheib?tl=<estimate_key>#insights opens the
  // drawer straight away. Also how the headless screenshots are taken.
  const tlAuto = new URLSearchParams(window.location.search).get('tl');
  if (tlAuto) window.openTimeline(tlAuto);

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
      if (viewLogs && !viewLogs.hidden) loadTwilioMessages();
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

    // WNC-01: wire the auto-send toggle. Only fires a POST when the change
    // is user-initiated (not the programmatic poll sync which sets dataset.syncing).
    const autoSendCheckbox = document.getElementById('auto-send-checkbox');
    const autoSendLabel    = document.getElementById('auto-send-label');
    if (autoSendCheckbox) {
      autoSendCheckbox.addEventListener('change', async () => {
        if (autoSendCheckbox.dataset.syncing) return; // poll sync — no POST
        const desired = autoSendCheckbox.checked;
        const prior   = !desired; // current checked state before user click
        try {
          const resp = await fetch(`${API_BASE}/auto-send`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ enabled: desired }),
          });
          if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
          if (autoSendLabel) {
            autoSendLabel.textContent = desired
              ? 'Auto-send ON — texts go out automatically'
              : 'Auto-send OFF — paused; manual Send now still works';
          }
        } catch (_) {
          // Revert to prior state; next diagnostic poll will reconcile.
          autoSendCheckbox.dataset.syncing = '1';
          autoSendCheckbox.checked = prior;
          delete autoSendCheckbox.dataset.syncing;
        }
      });
    }

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
