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
  // keeps its content, and the answer is dropped. A read-only disclosure with a data-key
  // does not hold the refresh; the refresh opens it again in the new list. Sessions
  // selected for a bulk action hold it too, so the selection stays (#429). A field that
  // holds input not yet sent holds it also without focus, so a note typed on a card stays
  // after a click elsewhere, such as on the card's Copy button (#510).
  function unsent() {
    var fields = list.querySelectorAll("input, textarea, select");
    for (var i = 0; i < fields.length; i++) {
      var f = fields[i];
      if (f.tagName === "SELECT") {
        for (var j = 0; j < f.options.length; j++) {
          if (f.options[j].selected !== f.options[j].defaultSelected) {
            return true;
          }
        }
      } else if (f.type === "checkbox" || f.type === "radio") {
        if (f.checked !== f.defaultChecked) {
          return true;
        }
      } else if (f.type !== "hidden" && f.value !== f.defaultValue) {
        return true;
      }
    }
    return false;
  }
  function busy() {
    var active = document.activeElement;
    return list.querySelector("details[open]:not([data-key]), details[open] form, form[data-busy], input[form=bulk]:checked") !== null ||
      (active !== null && list.contains(active) && /^(INPUT|TEXTAREA|SELECT)$/.test(active.tagName)) || unsent();
  }
  // held marks a list that a refresh skipped for unsent input, so the page says it is not
  // current; the mark goes when the input is sent or cleared.
  function held(on) {
    if (on) {
      list.setAttribute("data-held", "true");
    } else {
      list.removeAttribute("data-held");
    }
  }
  function refresh() {
    if (document.visibilityState !== "visible") {
      return;
    }
    held(unsent());
    if (busy()) {
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
          held(unsent());
          return;
        }
        var open = Array.prototype.map.call(list.querySelectorAll("details[open][data-key]"), function (d) {
          return d.dataset.key;
        });
        list.innerHTML = html;
        list.querySelectorAll("details[data-key]").forEach(function (d) {
          if (open.indexOf(d.dataset.key) >= 0) {
            d.open = true;
          }
        });
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
