// Private text is saved only by explicit opt-in. Pending receipts store metadata only.
(function () {
  "use strict";
  if (!document.body || document.body.dataset.remote !== "true") { return; }
  var session = document.body.dataset.remoteSession;
  var prefix = "koinon.remote.v2:" + window.location.origin + ":";
  var week = 7 * 86400000, pending = [], checking = false;
  // Receipt timestamps come from the host. The phone wall clock is only for
  // local draft ordering; monotonic elapsed time tolerates phone clock changes.
  var hostAnchor = Number(document.body.dataset.hostTime), elapsedAnchor = performance.now();
  function hostNow() { return hostAnchor + Math.max(0, performance.now() - elapsedAnchor); }
  function observeHost(value) {
    if (Number.isFinite(value) && value > 0) { hostAnchor = value; elapsedAnchor = performance.now(); }
  }
  function hasHostTime(operation) { return Number.isFinite(operation.hostAt) && operation.hostAt > 0; }
  function operationExpired(operation) { return hasHostTime(operation) && hostNow() - operation.hostAt >= week; }
  var status = document.getElementById("recovery-status");
  function tell(text) { if (status) { status.textContent = text; } }
  function read(key, fallback) {
    try { var value = window.localStorage.getItem(prefix + key); var decoded = value ? JSON.parse(value) : fallback;
      if (Array.isArray(fallback) && !Array.isArray(decoded)) { throw new Error("invalid browser recovery state"); }
      return decoded; }
    catch (_) { tell("Browser storage unavailable; draft recovery is unavailable. Forms still work."); return fallback; }
  }
  function write(key, value) {
    try { window.localStorage.setItem(prefix + key, JSON.stringify(value)); return true; }
    catch (_) { tell("Browser storage unavailable; draft recovery is unavailable. Keep this tab open."); return false; }
  }
  function remove(key) { try { window.localStorage.removeItem(prefix + key); } catch (_) { tell("Browser storage could not be cleared."); } }
  function clearDrafts() { remove("drafts"); }
  function opted() { return read("save-drafts", false) === true; }
  function drafts() {
    return read("drafts", []).filter(function (d) { return d && typeof d.text === "string" && d.at <= Date.now() && Date.now() - d.at < week; });
  }
  function route() { return window.location.pathname + window.location.search; }
  function targetOf(form) {
    var fields = new FormData(form);
    return fields.get("reference") || fields.get("room_id") || fields.get("to") || "";
  }
  function fieldKey(field) { return [session, route(), field.form.getAttribute("action") || "decision", targetOf(field.form), field.name].join("|"); }
  function eligible(field) {
    if (!field || field.tagName !== "TEXTAREA" || !field.form) { return false; }
    var action = field.form.getAttribute("action") || "";
    if (field.name === "body") { return action === "/dashboard/actions/send" || action === "/dashboard/actions/room-post"; }
    return field.name === "note" && window.location.pathname === "/dashboard/decisions" && (!action || /^\/dashboard\/actions\/decision-/.test(action));
  }
  function save(field) {
    if (!opted() || !eligible(field)) { return; }
    var key = fieldKey(field), rows = drafts().filter(function (d) { return d.key !== key; });
    if (field.value) { rows.push({key:key, session:session, route:route(), target:targetOf(field.form), name:field.name, text:field.value, at:Date.now()}); }
    write("drafts", rows);
  }
  function recover() {
    if (!opted()) { return; }
    var rows = drafts();
    write("drafts", rows);
    document.querySelectorAll("textarea[name=body], textarea[name=note]").forEach(function (field) {
      if (!eligible(field) || field.value || field.dataset.recoverOffered) { return; }
      var matches = rows.filter(function (d) { return d.route === route() && d.target === targetOf(field.form) && d.name === field.name; });
      if (!matches.length) { return; }
      var draft = matches[matches.length - 1];
      var button = document.createElement("button"); button.type = "button";
      button.textContent = "Recover draft" + (draft.session !== session ? " from previous sign-in" : "");
      button.addEventListener("click", function () { field.value = draft.text; button.remove(); delete field.dataset.recoverOffered; save(field); });
      field.dataset.recoverOffered = "true"; field.insertAdjacentElement("afterend", button);
    });
  }
  var toggle = document.getElementById("save-drafts");
  if (toggle) {
    toggle.checked = opted();
    toggle.addEventListener("change", function () { write("save-drafts", toggle.checked); if (!toggle.checked) { clearDrafts(); } else { recover(); } });
  }
  var clear = document.getElementById("clear-drafts");
  if (clear) { clear.addEventListener("click", function () { clearDrafts(); tell("Saved drafts cleared."); }); }
  document.addEventListener("input", function (event) { save(event.target); });
  document.addEventListener("change", function (event) {
    if (event.target.form) { event.target.form.querySelectorAll("textarea").forEach(save); }
  });
  function supported(path) { return /^\/dashboard\/actions\/(send|room-post|decision-(approve|feedback|complete|decline|acknowledge))$/.test(path); }
  function savePending() { write("pending", pending); }
  pending = read("pending", []).filter(function (p) { return p && /^[a-f0-9-]{16,80}$/.test(p.id) && Number.isFinite(p.at); });
  function showPending() {
    var panel = document.getElementById("attention-pending-actions");
    if (!panel) { return; }
    panel.replaceChildren();
    pending.forEach(function (p) {
      var line = document.createElement("p");
      line.textContent = "Result unknown · " + p.id + " · " + p.type + " · " + (!hasHostTime(p) ? "Original host time unavailable. Inspect target state before another action." : operationExpired(p) ? "Receipt window expired. Inspect target state before another action." : "Check the host receipt; no automatic retry.");
      var inspect = document.createElement("a"); inspect.href = p.type.indexOf("decision-") >= 0 ? "/dashboard/decisions?ref=" + encodeURIComponent(p.target) : p.type.indexOf("room-post") >= 0 ? "/dashboard/rooms?room=" + encodeURIComponent(p.target) : "/dashboard/messages?to=" + encodeURIComponent(p.target); inspect.textContent = "Inspect target"; line.append(inspect);
      var clear = document.createElement("button"); clear.type = "button"; clear.textContent = "I inspected the target; dismiss recovery";
      clear.addEventListener("click", function () { pending = pending.filter(function (q) { return q.id !== p.id; }); savePending(); showPending(); }); line.append(clear); panel.append(line);
    });
  }
  function resolve() {
    if (checking || !pending.length || document.visibilityState !== "visible") { return; }
    checking = true;
    Promise.all(pending.map(function (operation) {
      return fetch("/dashboard/receipts?id=" + encodeURIComponent(operation.id), {credentials:"same-origin", cache:"no-store"})
        .then(function (response) { if (response.status === 401) { unauthorized(response); } if (!response.ok) { throw new Error("unavailable"); } return response.json(); })
        .then(function (receipt) {
          observeHost(receipt.server_time);
          if (receipt.found === false && receipt.authoritative) {
            operation.missing = !receipt.expired && hasHostTime(operation) && !operationExpired(operation);
            tell(!hasHostTime(operation) ? "Result unknown. Original host time unavailable; inspect the target and dismiss recovery before another action." : "Result unknown. The host has no receipt for " + operation.id + ". Inspect the target; explicitly submit the unchanged form to retry with this ID within seven days.");
          } else if (receipt.found !== false && (receipt.state || (receipt.receipt && receipt.receipt.state))) {
            var record = receipt.receipt || receipt;
            if (record.state === "accepted" && !operation.disputed) {
              write("drafts", drafts().filter(function (d) { return d.target !== operation.target || d.route !== operation.route || d.at > operation.at; }));
            }
            tell((record.state === "conflict" ? "Conflict" : "Accepted") + " · " + operation.id + ". Accepted means recorded by the host; delivery and acknowledgement are separate.");
            pending = pending.filter(function (p) { return p.id !== operation.id; });
          }
        }).catch(function () { tell("Result unknown; receipt lookup unavailable. No action was replayed."); });
    })).finally(function () { checking = false; savePending(); showPending(); });
  }
  function authenticated(force) {
    // A fresh CSRF token can be applied without replacing or submitting any form.
    if (authenticated.busy || (!force && authenticated.at && Date.now() - authenticated.at < 15000)) { return; }
    authenticated.busy = true;
    fetch("/dashboard/session", {credentials:"same-origin", cache:"no-store"})
      .then(function (response) { if (response.status === 401) { unauthorized(response); } if (!response.ok) { throw new Error("session unavailable"); } return response.json(); })
      .then(function (data) {
        observeHost(data.server_time);
        if (typeof data.csrf !== "string") { return; }
        document.querySelectorAll('input[name="csrf"]').forEach(function (field) { field.value = data.csrf; field.defaultValue = data.csrf; });
        if (data.session_id) { session = data.session_id; }
        authenticated.at = Date.now(); recover(); resolve();
      }).catch(function () {}).finally(function () { authenticated.busy = false; });
  }
  function unauthorized(response) {
    var reason = response.headers && response.headers.get("X-Koinon-Auth-Reason");
    if (/^(device_revoked|password_changed|remote_disabled|origin_changed)$/.test(reason || "")) { clearDrafts(); }
  }
  window.koinonRecovery = {
    authenticated:authenticated, unauthorized:unauthorized, resolve:resolve,
    prepare:function (form, target, body) {
      if (!supported(target.pathname)) { return null; }
      var targetID = body.get("reference") || body.get("room_id") || body.get("to") || "";
      var operation = pending.find(function (p) { return p.id === form.dataset.operationId || (p.type === target.pathname && p.target === targetID); });
      if (operation && !hasHostTime(operation)) { tell("Result unknown. Original host time unavailable; inspect the target and dismiss recovery before another action."); resolve(); return {blocked:true}; }
      if (operation && operationExpired(operation)) { tell("Receipt window expired. Inspect the target state and dismiss recovery in Attention before another action."); return {blocked:true}; }
      if (operation && !operation.missing) { tell("Result unknown. Check the host receipt before an explicit retry."); resolve(); return {blocked:true}; }
      if (!operation) {
        // Browser cryptographic randomness; never derive an operation ID from private text.
        if (!window.crypto || !window.crypto.randomUUID) { tell("Receipt recovery unavailable in this browser."); return null; }
        if (!Number.isFinite(hostAnchor) || hostAnchor <= 0) { tell("Host time unavailable. Refresh the page before submitting this action."); return {blocked:true}; }
        operation = {id:window.crypto.randomUUID(), at:Date.now(), hostAt:hostNow(), type:target.pathname, route:route(), target:body.get("reference") || body.get("room_id") || body.get("to") || ""};
        form.dataset.operationId = operation.id; pending.push(operation); savePending();
      }
      operation.missing = false; savePending();
      body.set("operation_id", operation.id); body.set("operation_created_at", String(Math.floor(operation.hostAt))); return operation;
    },
    conflict:function (operation) { if (operation) { operation.disputed = true; savePending(); } },
    completed:function (form, target, response, operation) {
      if (target.pathname === "/dashboard/logout") { clearDrafts(); remove("pending"); return; }
      // A redirect with a notice can be a refusal; only accepted receipts clear drafts.
      if (!operation) { return; }
      return fetch("/dashboard/receipts?id=" + encodeURIComponent(operation.id), {credentials:"same-origin", cache:"no-store"})
        .then(function (r) { if (!r.ok) { throw new Error("unavailable"); } return r.json(); })
        .then(function (data) {
          var receipt = data.receipt || data;
          if (receipt.state !== "accepted") { return; }
          var target = targetOf(form);
          write("drafts", drafts().filter(function (d) { return d.route !== route() || d.target !== target || d.at > operation.at; }));
          pending = pending.filter(function (p) { return p.id !== operation.id; }); savePending(); delete form.dataset.operationId;
        }).catch(function () {});
    }
  };
  recover(); showPending(); resolve();
  window.addEventListener("online", resolve);
})();
