// Remote setup status is an independent read. Account approval never submits an
// enable action; the maintainer presses Enable after returning from Tailscale.
(function () {
  "use strict";
  var panel = document.getElementById("remote-panel");
  if (!panel) { return; }
  var status = document.getElementById("remote-read-status");
  var pending = false, checked = new URL(window.location.href).searchParams.get("remote_setup") === "1";
  var refreshed = 0;
  function busy() {
    return panel.querySelector("form[data-busy], details[open]") ||
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
        // Preserve a provider approval link across ordinary status reads without
        // querying the provider again. All measured status values still update.
        var approval = !explicit && panel.querySelector("#remote-approval");
        panel.innerHTML = html;
        var replacement = panel.querySelector("#remote-approval");
        if (approval && replacement) { replacement.replaceWith(approval); }
        if (explicit) { checked = true; }
        refreshed = Date.now();
        status.textContent = "Remote status checked " + new Date(refreshed).toLocaleTimeString();
      }).catch(function () {
        status.textContent = "Remote status unavailable; displayed values may be stale. Check your connection and sign-in, then check again. Writes are not retried.";
      }).finally(function () { clearTimeout(timeout); pending = false; });
  }
  document.addEventListener("click", function (event) {
    var link = event.target.closest("a[data-remote-check]");
    if (!link) { return; }
    event.preventDefault(); read(true);
  });
  window.addEventListener("online", function () { read(false); });
  document.addEventListener("visibilitychange", function () { read(false); });
  window.addEventListener("offline", function () { status.textContent = "Connection unavailable. Remote status may be stale."; });
  window.setInterval(function () {
    if (refreshed && Date.now() - refreshed > 30000) { status.textContent = "Remote status may be stale. Check again for current values."; }
    read(false);
  }, 15000);
  read(false);
})();
