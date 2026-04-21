// Earl Scheib Concord — Queue Admin UI
// Vanilla JS, ~200 lines. No framework, no bundler.
// Talks only to the local Go proxy: /api/queue, /api/cancel, /alive.

(function () {
  'use strict';

  const REFRESH_MS   = 15000;
  const HEARTBEAT_MS = 10000;
  const UNDO_MS      = 5000;

  // Pacific-time formatter — e.g. "Tue 2:30 PM"
  const timeFmt = new Intl.DateTimeFormat('en-US', {
    timeZone: 'America/Los_Angeles',
    weekday: 'short',
    hour: 'numeric',
    minute: '2-digit'
  });

  const JOB_TYPE_LABELS = {
    '24h':    '24-HOUR',
    '3day':   '3-DAY',
    'review': 'REVIEW'
  };

  // Active undo timers, keyed by job id.
  // Clicking the pill aborts (clearTimeout + removes row .cancelling + hides pill).
  const pendingUndos = new Map(); // id -> { timerId, rowEl, pillEl }

  const queueEl         = document.getElementById('queue');
  const refreshCaption  = document.getElementById('refresh-caption');
  const refreshBtn      = document.getElementById('refresh-btn');
  const cardTpl         = document.getElementById('customer-card-template');
  const rowTpl          = document.getElementById('message-row-template');
  const emptyTpl        = document.getElementById('empty-state-template');
  const errorTpl        = document.getElementById('error-state-template');

  let lastFetchedAt = 0;

  // ---------- Rendering ------------------------------------------------

  function renderQueue(jobs) {
    queueEl.innerHTML = '';
    queueEl.setAttribute('aria-busy', 'false');

    if (!jobs || jobs.length === 0) {
      queueEl.appendChild(emptyTpl.content.cloneNode(true));
      return;
    }

    // Group by phone
    const groups = new Map(); // phone -> {name, createdAtMax, jobs[]}
    for (const j of jobs) {
      const key = j.phone || `id-${j.id}`;
      if (!groups.has(key)) {
        groups.set(key, { phone: j.phone, name: j.name, createdAtMax: j.created_at, jobs: [] });
      }
      const g = groups.get(key);
      g.jobs.push(j);
      if (j.created_at > g.createdAtMax) {
        g.createdAtMax = j.created_at;
        g.name = j.name; // most recent name wins
      }
    }

    const sortedGroups = Array.from(groups.values())
      .sort((a, b) => b.createdAtMax - a.createdAtMax);

    sortedGroups.forEach((g, i) => {
      g.jobs.sort((a, b) => a.send_at - b.send_at);
      queueEl.appendChild(buildCustomerCard(g, i));
    });
  }

  function buildCustomerCard(group, index) {
    const frag = cardTpl.content.cloneNode(true);
    const article = frag.querySelector('.customer');
    article.style.setProperty('--i', String(index));
    article.dataset.phone = group.phone || '';

    frag.querySelector('.customer-name').textContent  = group.name || 'Unknown';
    frag.querySelector('.customer-phone').textContent = formatPhone(group.phone);

    const ul = frag.querySelector('.messages');
    for (const job of group.jobs) {
      ul.appendChild(buildMessageRow(job));
    }
    return frag;
  }

  function buildMessageRow(job) {
    const frag = rowTpl.content.cloneNode(true);
    const li = frag.querySelector('.message');
    li.dataset.jobId = String(job.id);

    frag.querySelector('.send-time').textContent = formatSendTime(job.send_at);
    const typeEl = frag.querySelector('.job-type');
    typeEl.textContent = JOB_TYPE_LABELS[job.job_type] || job.job_type;
    frag.querySelector('.job-ref').textContent = job.doc_id || '';

    const cancelBtn = frag.querySelector('.cancel-btn');
    const pill = frag.querySelector('.undo-pill');
    cancelBtn.addEventListener('click', () => armCancel(job.id, li, pill));
    pill.addEventListener('click', () => abortCancel(job.id));
    return frag;
  }

  function formatSendTime(unixSeconds) {
    if (!unixSeconds) return '';
    return timeFmt.format(new Date(unixSeconds * 1000));
  }

  function formatPhone(raw) {
    if (!raw) return '';
    // +15551234567 -> +1 (555) 123-4567
    const d = raw.replace(/[^\d]/g, '');
    if (d.length === 11 && d.startsWith('1')) {
      return `+1 (${d.slice(1,4)}) ${d.slice(4,7)}-${d.slice(7)}`;
    }
    return raw;
  }

  // ---------- Cancel flow ---------------------------------------------

  function armCancel(jobId, rowEl, pillEl) {
    if (pendingUndos.has(jobId)) return; // already armed
    rowEl.classList.add('cancelling');
    pillEl.hidden = false;
    // Restart the conic-gradient animation by cloning the ring element:
    const ring = pillEl.querySelector('.undo-ring');
    if (ring) {
      ring.style.animation = 'none';
      // Force reflow then restore the animation
      void ring.offsetWidth;
      ring.style.animation = '';
    }
    const timerId = setTimeout(() => fireCancel(jobId), UNDO_MS);
    pendingUndos.set(jobId, { timerId, rowEl, pillEl });
  }

  function abortCancel(jobId) {
    const entry = pendingUndos.get(jobId);
    if (!entry) return;
    clearTimeout(entry.timerId);
    entry.rowEl.classList.remove('cancelling');
    entry.pillEl.hidden = true;
    pendingUndos.delete(jobId);
  }

  async function fireCancel(jobId) {
    const entry = pendingUndos.get(jobId);
    if (!entry) return; // aborted at the exact same tick

    try {
      const resp = await fetch('/api/cancel', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ id: jobId })
      });
      if (resp.ok) {
        // Collapse row
        entry.rowEl.style.transition = 'opacity 200ms ease';
        entry.rowEl.style.opacity = '0';
        setTimeout(() => entry.rowEl.remove(), 220);
      } else {
        // Show inline error, revert row
        const errJson = await resp.json().catch(() => ({}));
        const msg = errJson.error || `cancel failed (${resp.status})`;
        revertWithError(entry, msg);
      }
    } catch (e) {
      revertWithError(entry, 'network error — please retry');
    } finally {
      pendingUndos.delete(jobId);
    }
  }

  function revertWithError(entry, msg) {
    entry.rowEl.classList.remove('cancelling');
    entry.pillEl.hidden = true;
    const lbl = entry.pillEl.querySelector('.undo-label');
    if (lbl) lbl.textContent = 'cancelled — click to undo';
    // Flash an inline error sentence by injecting a temporary <span> in the row
    const err = document.createElement('span');
    err.className = 'inline-error';
    err.textContent = msg;
    err.style.gridColumn = '1 / -1';
    err.style.color = 'var(--oxblood)';
    err.style.fontSize = '12px';
    err.style.paddingTop = '6px';
    entry.rowEl.appendChild(err);
    setTimeout(() => err.remove(), 4000);
  }

  // ---------- Data fetch + refresh ------------------------------------

  async function fetchQueue() {
    refreshCaption.textContent = 'refreshing…';
    if (refreshBtn) refreshBtn.classList.add('spinning');
    try {
      const resp = await fetch('/api/queue', { cache: 'no-store' });
      if (!resp.ok) {
        const errJson = await resp.json().catch(() => ({}));
        showError(errJson.error || `queue fetch failed (${resp.status})`);
        return;
      }
      const data = await resp.json();
      renderQueue(data);
      lastFetchedAt = Date.now();
      updateRefreshCaption();
    } catch (e) {
      showError('cannot reach local admin — is earlscheib.exe --admin still running?');
    } finally {
      setTimeout(() => refreshBtn && refreshBtn.classList.remove('spinning'), 500);
    }
  }

  function showError(msg) {
    queueEl.innerHTML = '';
    const frag = errorTpl.content.cloneNode(true);
    frag.querySelector('.error-msg').textContent = msg;
    queueEl.appendChild(frag);
  }

  function updateRefreshCaption() {
    if (!lastFetchedAt) return;
    const ago = Math.max(0, Math.round((Date.now() - lastFetchedAt) / 1000));
    refreshCaption.textContent = `updated ${ago}s ago`;
  }

  // ---------- Heartbeat ------------------------------------------------

  function sendAlive() {
    // Use fetch (not sendBeacon) — Chrome throttles sendBeacon in backgrounded
    // tabs, which would let our watchdog kill a perfectly good session just
    // because Marco alt-tabbed. fetch() with keepalive runs regardless.
    try {
      fetch('/alive', { method: 'POST', keepalive: true, body: '' }).catch(() => {});
    } catch (_) { /* never throw from heartbeat */ }
  }

  // ---------- Keyboard + click refresh --------------------------------

  function isEditingTarget(el) {
    if (!el) return false;
    const tag = (el.tagName || '').toLowerCase();
    return tag === 'input' || tag === 'textarea' || el.isContentEditable;
  }

  // ---------- Wire up --------------------------------------------------

  document.addEventListener('DOMContentLoaded', () => {
    fetchQueue();
    setInterval(fetchQueue, REFRESH_MS);
    setInterval(updateRefreshCaption, 1000);
    sendAlive();
    setInterval(sendAlive, HEARTBEAT_MS);

    // Also ping /alive on visibility change (browser tab foregrounded)
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

    // Best-effort: send one final /alive when tab closes so server can
    // shut down faster than the heartbeat timeout would allow. Absence
    // of this still works — watchdog fires after 30s.
    window.addEventListener('pagehide', () => {
      // no-op: we do NOT send a "goodbye" beacon; letting the 30s watchdog
      // expire is intentional per CONTEXT.md design.
    });
  });
})();
