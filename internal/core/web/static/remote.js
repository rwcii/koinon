// Remote setup status is an independent read. Account approval never submits an
// enable action; the maintainer presses Enable after returning from Tailscale.
(function () {
  "use strict";
  var panel = document.getElementById("remote-panel");
  if (!panel) { return; }
  var status = document.getElementById("remote-read-status");
  var pending = false, checked = new URL(window.location.href).searchParams.get("remote_setup") === "1";
  var refreshed = 0, ticketBusy = false, ticketURL = "", ticketDeadline = 0;
  // Opening Go remote focuses setup; other Settings sections stay available.
  if (new URL(window.location.href).hash === "#remote-access") {
    document.querySelectorAll("details.action").forEach(function (section) {
      if (section.id !== "remote-access") { section.open = false; }
    });
  }
  function discardTicket() {
    if (ticketURL) { URL.revokeObjectURL(ticketURL); }
    ticketURL = ""; ticketDeadline = 0;
  }
  function ticketMessage(text, failed) {
    var result = document.getElementById("remote-ticket-result");
    if (result) { result.textContent = text; result.hidden = !text; }
    if (failed) {
      var output = document.getElementById("remote-ticket-output");
      if (output) { output.hidden = true; }
    }
  }
  function createTicket(button) {
    if (ticketBusy) { return; }
    ticketBusy = true; button.disabled = true;
    ticketMessage("Creating your code…", false);
    var started = Date.now(), controller = new AbortController();
    var timeout = setTimeout(function () { controller.abort(); }, 10000);
    fetch("/dashboard/remote/ticket", {
      method: "POST", body: new URLSearchParams({ csrf: button.dataset.csrf }),
      credentials: "same-origin", cache: "no-store", redirect: "error", signal: controller.signal
    }).then(function (response) {
      if (!response.ok) { throw new Error("ticket unavailable"); }
      var lifetime = Number(response.headers.get("X-Koinon-Ticket-Lifetime"));
      if (!Number.isFinite(lifetime) || lifetime <= 0 || lifetime > 300) { throw new Error("invalid expiry"); }
      return response.blob().then(function (blob) { return { blob: blob, deadline: started + lifetime * 1000 }; });
    }).then(function (ticket) {
      if (ticket.blob.type !== "image/png" || ticket.deadline <= Date.now()) { throw new Error("code expired"); }
      discardTicket();
      ticketURL = URL.createObjectURL(ticket.blob); ticketDeadline = ticket.deadline;
      document.getElementById("remote-ticket-image").src = ticketURL;
      document.getElementById("remote-ticket-output").hidden = false;
      document.getElementById("remote-ticket-message").textContent = "Scan with your phone. No password needed. One use, expires in 2 minutes.";
      button.textContent = "New code";
      ticketMessage("", false);
    }).catch(function () {
      discardTicket();
      ticketMessage("Couldn’t create a code. Check your connection and sign-in, then try again.", true);
    }).finally(function () { clearTimeout(timeout); ticketBusy = false; button.disabled = false; });
  }

  function busy() {
    return ticketBusy || panel.querySelector("form[data-busy], details[open]") ||
      (panel.contains(document.activeElement) && /^(INPUT|TEXTAREA|SELECT)$/.test(document.activeElement.tagName));
  }
  function read(explicit) {
    if (pending || document.visibilityState !== "visible" || busy()) { return; }
    pending = true;
    var controller = new AbortController();
    var timeout = setTimeout(function () { controller.abort(); }, 6000);
    // Provider account instructions are queried only on an explicit check. Normal
    // background reads do not repeatedly contact the feature approval service.
    var url = "/dashboard/remote/status?fragment=1" + (explicit ? "&setup=1" : checked ? "&checked=1" : "");
    fetch(url, { credentials: "same-origin", cache: "no-store", redirect: "error", signal: controller.signal })
      .then(function (response) {
        if (!response.ok) { throw new Error("status unavailable"); }
        return response.text();
      }).then(function (html) {
        if (busy()) { return; }
        // Preserve the last explicit setup result, including enable eligibility,
        // without re-querying account instructions. Only the measured status
        // updates on ordinary reads. Enabled state removes the setup section.
        var setup = checked && !explicit && panel.querySelector("#remote-setup");
        var ticket = panel.querySelector("#remote-ticket");
        panel.innerHTML = html;
        var replacement = panel.querySelector("#remote-setup");
        if (setup && replacement) { replacement.replaceWith(setup); }
        var ticketReplacement = panel.querySelector("#remote-ticket");
        if (ticket && ticketReplacement) { ticketReplacement.replaceWith(ticket); }
        else if (!ticketReplacement) { discardTicket(); }
        if (explicit) { checked = true; }
        refreshed = Date.now();
        status.textContent = "Remote status checked " + new Date(refreshed).toLocaleTimeString();
        status.hidden = true;
      }).catch(function () {
        status.textContent = "Connection unavailable. These details may be stale. Check your internet and try again.";
        status.hidden = false;
      }).finally(function () { clearTimeout(timeout); pending = false; });
  }
  document.addEventListener("click", function (event) {
    var link = event.target.closest("a[data-remote-check]");
    if (link) { event.preventDefault(); read(true); return; }
    var button = event.target.closest("button[data-remote-ticket]");
    if (button) { event.preventDefault(); createTicket(button); }
  });
  window.addEventListener("online", function () { read(false); });
  document.addEventListener("visibilitychange", function () { read(false); });
  window.addEventListener("offline", function () { status.textContent = "Connection unavailable. These details may be stale."; status.hidden = false; });
  window.setInterval(function () {
    if (refreshed && Date.now() - refreshed > 30000) { status.textContent = "Connection details may be stale. Check again to update them."; status.hidden = false; }
    read(false);
  }, 15000);
  window.setInterval(function () {
    if (!ticketDeadline || Date.now() < ticketDeadline) { return; }
    discardTicket();
    var output = document.getElementById("remote-ticket-output");
    if (output) { output.hidden = true; }
    ticketMessage("Code expired. Get a new code to sign in.", false);
  }, 1000);
  window.addEventListener("pagehide", function () { discardTicket(); });
  read(false);
})();
