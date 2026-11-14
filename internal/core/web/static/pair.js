// A pairing ticket is in the fragment, never a request URL or saved bookmark.
// Redeem once; a failed response is never replayed on reconnect.
(function () {
  "use strict";
  var status = document.getElementById("pair-status");
  var help = document.getElementById("pair-help");
  var ticket = new URLSearchParams(window.location.hash.slice(1)).get("ticket");
  window.history.replaceState(null, "", "/dashboard/pair");
  function fail(text) { status.textContent = text; help.hidden = false; }
  if (!ticket || !/^[0-9a-f]{64}$/.test(ticket)) {
    fail("Scan a new sign-in code from your computer.");
    return;
  }
  var controller = new AbortController();
  var timeout = setTimeout(function () { controller.abort(); }, 10000);
  var body = new URLSearchParams({ ticket: ticket });
  ticket = "";
  fetch("/dashboard/pair", { method: "POST", body: body, credentials: "same-origin", cache: "no-store", redirect: "follow", signal: controller.signal })
    .then(function (response) {
      if (response.status === 401) { fail("This code has expired or was already used."); return; }
      // The device limit text is the host's own plain text; show it (#673 review L9).
      if (response.status === 409) { return response.text().then(function (text) { fail(text.trim() || "Signed-in device limit reached."); }); }
      if (!response.ok) { throw new Error("sign-in unavailable"); }
      var destination = new URL(response.url, window.location.href);
      if (!response.redirected || destination.origin !== window.location.origin || destination.pathname !== "/dashboard/attention" || destination.search || destination.hash) {
        throw new Error("unexpected sign-in destination");
      }
      window.location.replace(destination.toString());
    }).catch(function () { fail("Couldn’t confirm sign-in. Open the dashboard to check, or get a new code."); })
    .finally(function () { clearTimeout(timeout); });
})();
