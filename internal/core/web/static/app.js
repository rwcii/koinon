// Refresh the current list in place from a server-rendered fragment of the same view.
// The page works without this script; it only spares the manual reload.
(function () {
  "use strict";
  var list = document.getElementById("list");
  if (!list || !list.dataset.refresh) {
    return;
  }
  var interval = Number(list.dataset.refresh);
  // A refresh replaces the list, which would close an open disclosure (a purge
  // confirmation) and drop what was typed in a form inside it, so it waits meanwhile. It
  // checks again when the answer arrives: a form opened while the request was in flight
  // keeps its content, and the answer is dropped.
  function busy() {
    var active = document.activeElement;
    return list.querySelector("details[open]") !== null ||
      (active !== null && list.contains(active) && /^(INPUT|TEXTAREA|SELECT)$/.test(active.tagName));
  }
  function refresh() {
    if (document.visibilityState !== "visible" || busy()) {
      return;
    }
    var url = new URL(window.location.href);
    url.searchParams.set("fragment", "1");
    fetch(url.toString(), { credentials: "same-origin", cache: "no-store", redirect: "error" })
      .then(function (response) {
        if (!response.ok) {
          throw new Error("refresh refused: " + response.status);
        }
        return response.text();
      })
      .then(function (html) {
        if (document.visibilityState !== "visible" || busy()) {
          return;
        }
        list.innerHTML = html;
        list.removeAttribute("data-stale");
      })
      .catch(function () {
        list.setAttribute("data-stale", "true");
      });
  }
  window.setInterval(refresh, interval);
  document.addEventListener("visibilitychange", refresh);
})();
