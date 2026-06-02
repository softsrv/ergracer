// ── Token expiry handling ─────────────────────────────────────────────────────

// When the server fires the token-expired event, attempt a silent refresh.
// If the refresh succeeds, replay the original request.
// If it fails, redirect to login.
document.body.addEventListener('token-expired', async function () {
  try {
    var res = await fetch('/auth/refresh', { method: 'POST' });
    if (res.ok) {
      // Replay by re-triggering HTMX on the active element.
      var active = document.querySelector('[hx-trigger]');
      if (active) htmx.trigger(active, 'retry');
    } else {
      window.location.href = '/login';
    }
  } catch (_) {
    window.location.href = '/login';
  }
});
