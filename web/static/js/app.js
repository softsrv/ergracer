// ── Token expiry handling ─────────────────────────────────────────────────────

// Tracks whichever element most recently triggered an htmx request, so a
// successful silent refresh (below) can replay that exact interaction rather
// than silently dropping it. Recorded via plain DOM events instead of any
// htmx-internal API/event-detail shape, so it keeps working across htmx
// version upgrades (this app vendors htmx 4.x, still in beta).
var lastHtmxTrigger = null;
document.addEventListener('submit', function (e) {
  if (e.target instanceof HTMLFormElement && (e.target.hasAttribute('hx-post') || e.target.hasAttribute('hx-get') || e.target.hasAttribute('hx-put') || e.target.hasAttribute('hx-patch') || e.target.hasAttribute('hx-delete'))) {
    lastHtmxTrigger = e.target;
  }
});
document.addEventListener('click', function (e) {
  var el = e.target.closest('[hx-get],[hx-post],[hx-put],[hx-patch],[hx-delete]');
  if (el && !(el instanceof HTMLFormElement)) {
    lastHtmxTrigger = el;
  }
});

// ── Prevent double-submission on plain (non-htmx) form posts ─────────────────

// htmx forms/buttons use hx-disable (see templates) to disable themselves for
// the duration of the request; that only applies to htmx-driven requests, so
// this is the equivalent safety net for a handful of plain <form method=post>
// submissions (e.g. sign-out) that don't go through htmx at all.
document.addEventListener('submit', function (e) {
  var form = e.target;
  if (!(form instanceof HTMLFormElement)) return;
  if (form.hasAttribute('hx-post') || form.hasAttribute('hx-get') || form.hasAttribute('hx-put') || form.hasAttribute('hx-patch') || form.hasAttribute('hx-delete')) return;
  if (form.method.toLowerCase() !== 'post') return;
  var btn = form.querySelector('button[type="submit"], input[type="submit"]');
  if (btn) btn.disabled = true;
});

// When the server fires the token-expired event, attempt a silent refresh.
// If it succeeds, replay whichever element triggered the request that failed
// — re-dispatching its natural default event (submit for forms, click for
// everything else) so htmx's own listener picks it up and reissues the
// request normally. If the refresh fails, send the user back to the
// dashboard (the site's sole sign-in entry point via Discord OAuth).
document.body.addEventListener('token-expired', async function () {
  try {
    var res = await fetch('/auth/refresh', { method: 'POST' });
    if (res.ok) {
      if (lastHtmxTrigger instanceof HTMLFormElement) {
        lastHtmxTrigger.requestSubmit();
      } else if (lastHtmxTrigger) {
        lastHtmxTrigger.click();
      }
    } else {
      window.location.href = '/dashboard';
    }
  } catch (_) {
    window.location.href = '/dashboard';
  }
});

// ── Landing page: "Get Started" opens a login modal ──────────────────────────

// Jumping straight to Discord's consent screen on the first click felt
// unexpected for a visitor who might just be looking around, so "Get
// Started" opens this confirmation modal instead of linking directly.
(function () {
  var modal = document.getElementById('get-started-modal');
  if (!modal) return;
  document.querySelectorAll('.js-get-started').forEach(function (btn) {
    btn.addEventListener('click', function () {
      modal.showModal();
    });
  });
})();

// ── Profile page: "Add a server" modal ────────────────────────────────────────

// The modal offers two paths — install the bot elsewhere, or register into a
// server it's already in — and picking one reveals just that sub-section,
// following the same show/hide-by-class idiom as the rest of this file.
(function () {
  var modal = document.getElementById('add-server-modal');
  if (!modal) return;

  document.querySelectorAll('.js-add-server-modal').forEach(function (btn) {
    btn.addEventListener('click', function () {
      modal.showModal();
    });
  });

  modal.querySelectorAll('.js-add-choice').forEach(function (btn) {
    btn.addEventListener('click', function () {
      modal.querySelectorAll('.js-add-section').forEach(function (section) {
        section.classList.add('hidden');
      });
      var target = document.getElementById(btn.dataset.target);
      if (target) target.classList.remove('hidden');
    });
  });
})();
