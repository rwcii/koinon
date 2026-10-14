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
    return list.querySelector("details[open], form[data-busy]") !== null ||
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
  document.addEventListener("submit", function (event) {
    var form = event.target;
    if (!form.matches("form.verdict")) {
      return;
    }
    if (form.dataset.busy) {
      event.preventDefault();
      return;
    }
    form.dataset.busy = "true";
    // The buttons are disabled after the browser has read the form, so the chosen
    // action is still sent.
    window.setTimeout(function () {
      form.querySelectorAll("button").forEach(function (b) { b.disabled = true; });
    }, 0);
  });
})();
