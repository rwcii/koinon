// Refresh the current list in place from a server-rendered fragment of the same view.
// The page works without this script; it only spares the manual reload.
(function () {
  "use strict";
  var list = document.getElementById("list");
  if (!list || !list.dataset.refresh) {
    return;
  }
  var interval = Number(list.dataset.refresh);
  function refresh() {
    if (document.visibilityState !== "visible") {
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
