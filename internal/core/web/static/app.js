// Refresh only reads. Forms and selected rows hold replacement in place; no draft is
// replaced while editing; reconnect never repeats an action.
(function () {
  "use strict";
  var list = document.getElementById("list");
  if (!list || !list.dataset.refresh) {
    return;
  }
  var connection = document.getElementById("connection");
  var state = document.getElementById("connection-state");
  var time = document.getElementById("refresh-time");
  var signIn = document.getElementById("sign-in");
  // Freshness uses elapsed browser time, independent of host/browser clock skew.
  var lastApplied = list.dataset.reload ? 0 : Date.now();
  var connected = !list.dataset.reload, expired = false, pending = false;
  var failures = 0, nextRead = 0;
  function loadedIndex(f) {
    var first = -1;
    for (var i = f.options.length - 1; i >= 0; i--) {
      if (f.options[i].defaultSelected) { return i; }
      if (!f.options[i].disabled) { first = i; }
    }
    return f.multiple ? -1 : first;
  }
  function unsent() {
    var fields = list.querySelectorAll("input, textarea, select");
    for (var i = 0; i < fields.length; i++) {
      var f = fields[i];
      if (f.tagName === "SELECT") {
        if (f.selectedIndex !== loadedIndex(f)) { return true; }
        for (var j = 0; f.multiple && j < f.options.length; j++) {
          if (f.options[j].selected !== f.options[j].defaultSelected) { return true; }
        }
      } else if (f.type === "checkbox" || f.type === "radio") {
        if (f.checked !== f.defaultChecked) { return true; }
      } else if (f.type !== "hidden" && f.value !== f.defaultValue) { return true; }
    }
    return false;
  }
  function busy() {
    var active = document.activeElement;
    return list.querySelector("details[open]:not([data-key]), details[open] form, form[data-busy], input[form=bulk]:checked") !== null ||
      (active !== null && list.contains(active) && /^(INPUT|TEXTAREA|SELECT)$/.test(active.tagName)) || unsent();
  }
  function held(on) {
    if (on) { list.setAttribute("data-held", "true"); }
    else { list.removeAttribute("data-held"); }
  }
  function status() {
    var stale = Date.now() - lastApplied >= 15000;
    if (stale) { list.setAttribute("data-stale", "true"); }
    else { list.removeAttribute("data-stale"); }
    if (state) {
      state.textContent = (expired ? "Sign-in required" : connected ? "Connected" : "Cannot reach host · Connection unavailable") +
        (stale ? " · Stale data" : "") + (busy() ? " · Form kept; display refresh paused" : "");
    }
    if (connection) { connection.dataset.state = expired || !connected ? "unavailable" : stale ? "stale" : "connected"; }
    if (signIn) { signIn.hidden = !expired; }
  }
  function refresh() {
    status();
    if (document.visibilityState !== "visible" || pending || Date.now() < nextRead) { return; }
    held(unsent());
    var url = new URL(window.location.href);
    url.searchParams.set("fragment", "1");
    pending = true;
    var controller = new AbortController();
    var timeout = window.setTimeout(function () { controller.abort(); }, 10000);
    fetch(url.toString(), { credentials: "same-origin", cache: "no-store", redirect: "error", signal: controller.signal })
      .then(function (response) {
        expired = response.status === 401;
        if (expired && window.koinonRecovery) { window.koinonRecovery.unauthorized(response); }
        if (expired && signIn) {
          var back = new URL(window.location.href);
          back.searchParams.delete("fragment");
          signIn.href = "/dashboard/login?return=" + encodeURIComponent(back.pathname + back.search);
        }
        if (!response.ok) { throw new Error("refresh refused"); }
        return response.text();
      })
      .then(function (html) {
        connected = true; failures = 0; nextRead = 0;
        if (window.koinonRecovery) { window.koinonRecovery.authenticated(); }
        if (document.visibilityState !== "visible" || busy()) {
          held(unsent());
          return;
        }
        if (list.dataset.reload) { window.location.reload(); return; }
        var open = Array.prototype.map.call(list.querySelectorAll("details[open][data-key]"), function (d) { return d.dataset.key; });
        list.innerHTML = html;
        list.querySelectorAll("details[data-key]").forEach(function (d) {
          if (open.indexOf(d.dataset.key) >= 0) { d.open = true; }
        });
        lastApplied = Date.now();
        if (time) {
          time.dateTime = new Date(lastApplied).toISOString();
          time.textContent = new Date(lastApplied).toLocaleTimeString();
        }
      })
      .catch(function () {
        connected = false;
        var delays = [1000, 2000, 5000, 10000, 30000];
        var delay = delays[Math.min(failures++, delays.length - 1)] * (0.8 + Math.random() * 0.4);
        nextRead = Date.now() + delay;
        window.setTimeout(refresh, delay + 1);
      })
      .finally(function () { pending = false; window.clearTimeout(timeout); status(); });
  }
  window.setInterval(refresh, Number(list.dataset.refresh));
  window.setInterval(status, 1000);
  document.addEventListener("visibilitychange", function () { nextRead = 0; refresh(); });
  window.addEventListener("online", function () { nextRead = 0; refresh(); });
  window.addEventListener("offline", function () { connected = false; status(); });
  var reconnect = document.getElementById("reconnect");
  if (reconnect) { reconnect.addEventListener("click", function () { nextRead = 0; refresh(); }); }
  status();
})();
// Submit once and keep the current document if the response is lost. The server still
// performs POST then redirect, with its existing revisions and idempotency keys.
(function () {
  "use strict";
  document.addEventListener("submit", function (event) {
    var form = event.target;
    // Read the attributes: a form control named "action" or "method" hides the form's own
    // property of that name, as the Sessions bulk form's action select does (#654).
    if ((form.getAttribute("method") || "get").toLowerCase() !== "post") { return; }
    event.preventDefault();
    if (form.dataset.busy) { return; }
    var submitter = event.submitter;
    var target = new URL(submitter && submitter.getAttribute("formaction") || form.getAttribute("action") || window.location.href, window.location.href);
    if (target.origin !== window.location.origin || !target.pathname.startsWith("/dashboard/")) {
      var refusal = document.getElementById("action-result");
      if (refusal) { refusal.textContent = "This form target is unavailable. Reload the page before trying again."; refusal.hidden = false; }
      return;
    }
    var fields = new FormData(form);
    if (submitter && submitter.name) { fields.append(submitter.name, submitter.value); }
    var body = new URLSearchParams(fields);
    var recovery = window.koinonRecovery;
    var operation = recovery ? recovery.prepare(form, target, body) : null;
    if (operation && operation.blocked) { return; }
    var buttons = Array.prototype.map.call(form.querySelectorAll("button, input[type=submit]"), function (button) {
      var wasDisabled = button.disabled;
      button.disabled = true;
      return [button, wasDisabled];
    });
    form.dataset.busy = "true";
    var result = document.getElementById("action-result");
    function message(text) {
      if (result) { result.textContent = text; result.hidden = false; }
    }
    message(operation ? "Sending" : "Submitting…");
    var controller = new AbortController();
    var timeout = window.setTimeout(function () { controller.abort(); }, 20000);
    fetch(target.toString(), { method: "POST", body: body, credentials: "same-origin", cache: "no-store", redirect: "follow", signal: controller.signal })
      .then(function (response) {
        if (response.status === 401) {
          if (recovery) { recovery.unauthorized(response); }
          message(target.pathname === "/dashboard/login" ? "Sign-in failed. Check your Koinon password and try again." :
            "Sign-in required. Open Sign in again, then check the latest state. This action will not be replayed.");
          var link = document.getElementById("sign-in");
          if (link) {
            link.hidden = false;
            link.href = "/dashboard/login?return=" + encodeURIComponent(window.location.pathname + window.location.search);
          }
          return;
        }
        if (response.status === 403 && recovery) { recovery.authenticated(true); message("Form expired. Your text is kept; refresh the form token, then submit explicitly."); return; }
        if (response.status === 409 && target.pathname === "/dashboard/login" && typeof response.text === "function") {
          // The device limit text is the host's own plain text; show it (#673 review L9).
          return response.text().then(function (text) { message(text.trim() || "Signed-in device limit reached."); });
        }
        if (!response.ok) {
          message("The request was refused (HTTP " + response.status + "). Check the latest state before trying again.");
          return;
        }
        var replyURL = new URL(response.url || target.toString(), window.location.href);
        var code = replyURL.searchParams.get("notice");
        if (operation && replyURL.searchParams.has("notice") && !/^(sent|room_posted|decision_(approved|declined|feedback|completed|acknowledged))$/.test(code)) {
          if (/^(stale_revision|decided)$/.test(code)) {
            message("Conflict. Your text is kept. Inspect the current target before submitting another action.");
            if (recovery) { if (recovery.conflict) { recovery.conflict(operation); } recovery.resolve(); }
            return;
          }
          // Any other notice is the host's refusal: nothing was saved. Show its reason and
          // keep the text (#673 review M5).
          if (recovery && recovery.refused) { recovery.refused(operation); }
          var shown = typeof response.text === "function" ? response.text() : Promise.resolve("");
          return shown.then(function (html) {
            var reason = "";
            try {
              if (typeof DOMParser === "function") {
                var node = new DOMParser().parseFromString(html, "text/html").querySelector("p.notice");
                reason = node ? node.textContent : "";
              }
            } catch (error) { reason = ""; }
            message("Refused: " + (reason || code) + " Nothing was saved; your text is kept.");
          });
        }
        return Promise.resolve(recovery ? recovery.completed(form, target, response, operation) : null).then(function () {
        var destination = new URL(response.url, window.location.href);
        if (response.redirected && destination.origin === window.location.origin && destination.pathname.startsWith("/dashboard/")) {
          window.location.assign(destination.toString());
        } else if (target.pathname === "/dashboard/logout") {
          window.location.assign("/dashboard/login");
        } else {
          message("Result unknown; check the latest state before trying again.");
        }
        });
      })
      .catch(function () { message("Result unknown; check the latest state before trying again."); if (recovery && operation) { recovery.resolve(); } })
      .finally(function () {
        window.clearTimeout(timeout);
        delete form.dataset.busy;
        buttons.forEach(function (entry) { entry[0].disabled = entry[1]; });
      });
  });
})();
// Decision cards: copy a terminal-copy command, and keep a verdict form from a second
// submission while its request runs. Both use delegation, so refreshed cards keep them.
(function () {
  "use strict";
  document.addEventListener("click", function (event) {
    var button = event.target.closest("button.copy");
    if (!button) {
      return;
    }
    var code = button.parentElement.querySelector("code");
    if (!code || !navigator.clipboard) {
      button.textContent = "Copy failed";
      return;
    }
    navigator.clipboard.writeText(code.textContent).then(function () {
      button.textContent = "Copied";
    }, function () {
      button.textContent = "Copy failed";
    });
  });
})();
// Sessions view: "Select all sessions on this page" checks or clears every session box of
// the bulk form in the list, and shows whether all, some or none are checked (#429). It uses
// delegation, so a refreshed list keeps it.
(function () {
  "use strict";
  document.addEventListener("change", function (event) {
    var box = event.target;
    var list = document.getElementById("list");
    if (!list || !list.contains(box)) {
      return;
    }
    var rows = list.querySelectorAll("input[form=bulk][name=s]");
    var all = list.querySelector("input[data-select-all]");
    if (box === all) {
      rows.forEach(function (r) { r.checked = all.checked; });
      all.indeterminate = false;
    } else if (all && box.matches("input[form=bulk][name=s]")) {
      var checked = Array.prototype.filter.call(rows, function (r) { return r.checked; }).length;
      all.checked = checked > 0 && checked === rows.length;
      all.indeterminate = checked > 0 && checked < rows.length;
    }
  });
})();
