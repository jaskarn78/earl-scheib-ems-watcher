// Earl Scheib Concord — Queue Admin UI
// Vanilla JS. No framework, no bundler.
// Local Go proxy only: /api/queue, /api/cancel, /api/send-now, /alive.

(function () {
  'use strict';

  const REFRESH_MS   = 15000;
  const HEARTBEAT_MS = 10000;
  const DIAG_MS      = 5000;
  // OH4 OVERRIDE 2: when a poll fails, retry every 3s for 60s before
  // showing the "sleep" panel. Any successful poll in the retry window
  // dismisses the panel and resumes normal cadence.
  const RETRY_MS    = 3000;
  const RETRY_LIMIT = 20; // 20 * 3s = 60s retry window

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
  const cardTpl        = document.getElementById('job-card-template');
  const emptyTpl       = document.getElementById('empty-state-template');
  const errorTpl       = document.getElementById('error-state-template');
  const sleepTpl       = document.getElementById('sleep-state-template');

  let lastFetchedAt = 0;
  let retryAttempt  = 0;
  let inRetryMode   = false;
  let retryTimerId  = null;

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

  // ---------- Rendering ------------------------------------------------

  function renderQueue(jobs) {
    queueEl.innerHTML = '';
    queueEl.setAttribute('aria-busy', 'false');

    counters.pending = jobs ? jobs.length : 0;
    updateStats();

    if (!jobs || jobs.length === 0) {
      queueEl.appendChild(emptyTpl.content.cloneNode(true));
      return;
    }

    // Preserve server ordering (already ASC by send_at) — no regrouping.
    jobs.forEach((job, i) => {
      queueEl.appendChild(buildJobCard(job, i));
    });
  }

  function buildJobCard(job, index) {
    const frag = cardTpl.content.cloneNode(true);
    const article = frag.querySelector('.job-card');
    article.style.setProperty('--i', String(index));
    article.dataset.jobId = String(job.id);

    // Identity
    frag.querySelector('.job-name').textContent = job.name || 'Unknown customer';
    frag.querySelector('.job-phone').textContent = formatPhone(job.phone);
    const emailEl = frag.querySelector('.job-email');
    if (job.email) {
      emailEl.textContent = job.email;
    } else {
      emailEl.remove();
    }

    // Chip + scheduled send
    const chip = frag.querySelector('.job-chip');
    chip.textContent = JOB_TYPE_LABELS[job.job_type] || job.job_type;
    chip.setAttribute('data-type', job.job_type);

    frag.querySelector('.job-send-relative').textContent = formatRelative(job.send_at);
    frag.querySelector('.job-send-absolute').textContent = formatAbsolute(job.send_at);

    // Vehicle block
    const vehDescEl = frag.querySelector('.vehicle-desc');
    const vinEl = frag.querySelector('.vehicle-vin');
    const roEl = frag.querySelector('.vehicle-ro');
    vehDescEl.textContent = job.vehicle_desc || '';
    if (job.vin) {
      vinEl.textContent = maskVIN(job.vin);
      vinEl.setAttribute('title', job.vin); // hover-reveal full VIN
    }
    if (job.ro_id) {
      roEl.textContent = 'RO ' + job.ro_id;
    }
    // If the whole vehicle block is empty, drop the panel entirely
    // so there's no empty box on cards with no vehicle data.
    if (!job.vehicle_desc && !job.vin && !job.ro_id) {
      frag.querySelector('.job-vehicle').remove();
    }

    // SMS preview
    frag.querySelector('.sms-bubble').textContent = previewSMS(job.job_type, job.name);

    // Actions
    const sendBtn = frag.querySelector('.send-now-btn');
    const cancelBtn = frag.querySelector('.cancel-btn');
    const errEl = frag.querySelector('.job-error');

    sendBtn.addEventListener('click', () => handleSendNow(job, article, sendBtn, errEl));
    cancelBtn.addEventListener('click', () => handleCancel(job, article, cancelBtn, errEl));

    return frag;
  }

  function updateStats() {
    statPendingEl.textContent = String(counters.pending);
    statSentEl.textContent    = String(counters.sentToday);
    statFailedEl.textContent  = String(counters.failed);
  }

  // ---------- Send-now flow -------------------------------------------

  async function handleSendNow(job, cardEl, btnEl, errEl) {
    const name = job.name || 'this customer';
    const type = JOB_TYPE_LABELS[job.job_type] || job.job_type;
    const confirmMsg = `Send "${type}" SMS to ${name} right now?`;
    if (!window.confirm(confirmMsg)) return;

    btnEl.disabled = true;
    errEl.hidden = true;
    errEl.textContent = '';

    try {
      const resp = await fetch('/api/send-now', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ id: job.id }),
      });

      if (resp.status === 200) {
        counters.sentToday++;
        updateStats();
        markCardSent(cardEl);
        // Re-fetch so the row drops out of pending in the next tick.
        setTimeout(fetchQueue, 600);
        return;
      }

      if (resp.status === 404) {
        counters.failed++;
        updateStats();
        showCardError(errEl, 'Already sent or cancelled — refreshing list…');
        setTimeout(fetchQueue, 600);
        return;
      }

      const parsed = await resp.json().catch(() => ({}));
      const msg = parsed.error ? `Send failed: ${parsed.error}` : `Send failed (${resp.status})`;
      counters.failed++;
      updateStats();
      showCardError(errEl, msg);
      btnEl.disabled = false;

    } catch (e) {
      counters.failed++;
      updateStats();
      showCardError(errEl, 'Network error — please retry');
      btnEl.disabled = false;
    }
  }

  function markCardSent(cardEl) {
    const stamp = timeFmt.format(new Date());
    cardEl.setAttribute('data-sent-label', 'Sent at ' + stamp);
    cardEl.setAttribute('data-state', 'sent');
  }

  function showCardError(errEl, msg) {
    errEl.textContent = msg;
    errEl.hidden = false;
    setTimeout(() => { errEl.hidden = true; }, 5000);
  }

  // ---------- Cancel flow ---------------------------------------------

  async function handleCancel(job, cardEl, btnEl, errEl) {
    const name = job.name || 'this customer';
    const type = JOB_TYPE_LABELS[job.job_type] || job.job_type;
    if (!window.confirm(`Cancel the "${type}" follow-up for ${name}?`)) return;

    cardEl.setAttribute('data-state', 'cancelling');
    btnEl.disabled = true;
    errEl.hidden = true;

    try {
      const resp = await fetch('/api/cancel', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ id: job.id }),
      });
      if (resp.ok) {
        cardEl.style.transition = 'opacity 240ms ease, transform 240ms ease';
        cardEl.style.opacity = '0';
        cardEl.style.transform = 'translateY(-8px)';
        setTimeout(fetchQueue, 260);
      } else {
        const parsed = await resp.json().catch(() => ({}));
        const msg = parsed.error ? `Cancel failed: ${parsed.error}` : `Cancel failed (${resp.status})`;
        counters.failed++;
        updateStats();
        cardEl.setAttribute('data-state', 'pending');
        btnEl.disabled = false;
        showCardError(errEl, msg);
      }
    } catch (_) {
      counters.failed++;
      updateStats();
      cardEl.setAttribute('data-state', 'pending');
      btnEl.disabled = false;
      showCardError(errEl, 'Network error — please retry');
    }
  }

  // ---------- Data fetch + refresh ------------------------------------

  async function fetchQueue() {
    if (refreshBtn) refreshBtn.classList.add('spinning');
    try {
      const resp = await fetch('/api/queue', { cache: 'no-store' });
      if (!resp.ok) {
        const parsed = await resp.json().catch(() => ({}));
        showError(parsed.error || `queue fetch failed (${resp.status})`);
        handlePollFailure();
        return;
      }
      const data = await resp.json();
      // Success — exit retry mode if we were in it.
      onPollSuccess();
      renderQueue(data);
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
      const resp = await fetch('/api/diagnostic', { cache: 'no-store' });
      if (!resp.ok) return;
      const d = await resp.json();

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
    } catch (_) {
      // Transient errors at 5s poll cadence — silent by design.
    }
  }

  function setDiagText(id, txt) {
    const el = document.getElementById(id);
    if (el) el.textContent = txt;
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

  // ---------- Wire up --------------------------------------------------

  document.addEventListener('DOMContentLoaded', () => {
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
