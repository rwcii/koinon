(function () {
  "use strict";
  var container = document.getElementById("remote-devices");
  if (!container) { return; }
  var csrf = "", current = "";
  var status = document.getElementById("device-status");
  function text(tag, value) { var node = document.createElement(tag); node.textContent = value; return node; }
  function time(value) { return value ? new Date(value).toLocaleString() : "unknown"; }
  function action(kind, id, label) {
    status.textContent = "Saving…";
    return fetch("/dashboard/remote/devices", {method:"POST", credentials:"same-origin", cache:"no-store", body:new URLSearchParams({csrf:csrf, action:kind, id:id || "", label:label || ""})})
      .then(function (response) {
        if (response.status === 403) {
          // Refresh only credentials: retain label edits and require another explicit click.
          return readDevices().then(function () {
            status.textContent = "Form expired. Device controls refreshed; your label text is kept. Submit again explicitly.";
          });
        }
        if (!response.ok) { return response.text().then(function (message) { throw new Error(message); }); }
        if ((kind === "revoke" && id === current) || (kind === "revoke-all" && document.body.dataset.remote === "true")) {
          if (window.koinonRecovery) { window.koinonRecovery.unauthorized({headers:{get:function () { return "device_revoked"; }}}); }
          window.location.assign("/dashboard/login"); return;
        }
        status.textContent = "Saved"; load();
      }).catch(function (error) { status.textContent = error.message || "Result unknown. Refresh the device list before trying again."; });
  }
  function readDevices() {
    return fetch("/dashboard/remote/devices", {credentials:"same-origin", cache:"no-store"})
      .then(function (response) { if (!response.ok) { throw new Error("Signed-in device list unavailable (HTTP " + response.status + ")."); } return response.json(); })
      .then(function (data) {
        if (typeof data.csrf !== "string" || !data.csrf) { throw new Error("Device controls could not be refreshed."); }
        csrf = data.csrf; current = data.current && data.current.id;
        return data;
      });
  }
  function load() {
    return readDevices().then(function (data) {
        container.replaceChildren();
        (data.devices || []).forEach(function (device) {
          var row = document.createElement("section");
          row.append(text("h3", device.label + (device.this_browser ? " · This browser" : "")));
          row.append(text("p", "Created " + time(device.created_at) + " · Last observed " + time(device.last_observed) + " · Expires " + time(device.expires_at)));
          var name = document.createElement("input"); name.value = device.label; name.maxLength = 80; name.setAttribute("aria-label", "Device label"); row.append(name);
          var rename = text("button", "Rename"); rename.type = "button"; rename.addEventListener("click", function () { action("rename", device.id, name.value); }); row.append(rename);
          var forget = text("button", device.this_browser ? "Forget this phone" : "Forget browser"); forget.type = "button"; forget.addEventListener("click", function () { action("revoke", device.id); }); row.append(forget);
          container.append(row);
        });
        if (!(data.devices || []).length) { container.append(text("p", "No active remote browser sessions.")); }
        if (data.diagnostics) { container.append(text("p", "Private diagnostics: " + JSON.stringify(data.diagnostics))); }
      }).catch(function (error) { status.textContent = error.message; });
  }
  document.getElementById("revoke-all-devices").addEventListener("click", function () { action("revoke-all"); });
  load();
})();
