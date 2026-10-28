// Spoken alert (#553): while the switch on the Settings page is on, the browser says each
// decision or task that starts to wait for the maintainer, with its repository. The switch
// and the alert's state are this browser origin's own; the daemon speaks nothing.
(function () {
  "use strict";
  var list = document.getElementById("list");
  if (!list || !list.dataset.refresh) {
    return;
  }
  // STATE holds the version, the seen items and the pending sentences as one value, so a
  // write either moves all of them or none.
  var SWITCH = "koinon.speak", STATE = "koinon.speak.state";
  var speech = window.speechSynthesis;
  // The switch, the snapshot and the pending sentences are shared by the origin's tabs and
  // kept over reloads. The page keeps its own copy of each value it read or wrote, so storage
  // that fails later keeps them for this page only, and the Settings page says so.
  var memory = Object.create(null), shared = true;
  try {
    window.localStorage.setItem("koinon.speak.check", "1");
    window.localStorage.removeItem("koinon.speak.check");
  } catch (e) {
    shared = false;
  }
  function lost() {
    if (shared) {
      shared = false;
      limits();
    }
  }
  function get(key) {
    if (shared) {
      try {
        var value = window.localStorage.getItem(key);
        memory[key] = value === null ? undefined : JSON.parse(value);
      } catch (e) {
        lost();
      }
    }
    return memory[key];
  }
  function set(key, value) {
    memory[key] = value;
    if (shared) {
      try {
        if (value === undefined) {
          window.localStorage.removeItem(key);
        } else {
          window.localStorage.setItem(key, JSON.stringify(value));
        }
      } catch (e) {
        lost();
      }
    }
  }
  // locked runs task while no other tab of the origin runs one. Without Web Locks it runs at
  // once, and two tabs can both speak an alert.
  function locked(task) {
    if (navigator.locks && navigator.locks.request) {
      return navigator.locks.request("koinon-speak", task);
    }
    return Promise.resolve().then(task);
  }
  // state is the stored snapshot, or undefined for none or for a value of another shape, which
  // then gets a new baseline.
  function state() {
    var value = get(STATE);
    if (value && typeof value.version === "string" && Array.isArray(value.seen) && Array.isArray(value.pending)) {
      return value;
    }
    return undefined;
  }
  function on() {
    return speech !== undefined && get(SWITCH) === true;
  }
  function page(after) {
    return fetch("/dashboard/waiting" + (after ? "?after=" + after : ""), { credentials: "same-origin", cache: "no-store", redirect: "error" })
      .then(function (response) {
        if (!response.ok) {
          throw new Error("waiting refused: " + response.status);
        }
        return response.json();
      });
  }
  // snapshot reads every page of the waiting items with one decision version; a version that
  // changes between pages starts it again, up to three times. It gives null for no change
  // since state, and fails when no complete snapshot was read.
  function snapshot(state, tries) {
    return page(0).then(function (first) {
      if (state && state.version === first.version) {
        return null;
      }
      var items = first.items;
      function more(last) {
        if (!last.more) {
          return { version: first.version, items: items };
        }
        return page(last.next).then(function (next) {
          if (next.version !== first.version) {
            if (tries <= 1) {
              throw new Error("the waiting items kept changing");
            }
            return snapshot(state, tries - 1);
          }
          items = items.concat(next.items);
          return more(next);
        });
      }
      return more(first);
    });
  }
  function key(item) {
    return item.reference + "@" + item.revision;
  }
  // sentences says the new items, one sentence for each repository, in the order they came.
  function sentences(items) {
    var groups = [], byName = Object.create(null);
    items.forEach(function (item) {
      var group = byName[item.repository];
      if (!group) {
        group = byName[item.repository] = { name: item.repository, kinds: [] };
        groups.push(group);
      }
      group.kinds.push(item.kind);
    });
    return groups.map(function (group) {
      var what = group.kinds.length > 1 ? group.kinds.length + " items wait for you" : "a " + group.kinds[0] + " waits for you";
      return group.name ? group.name + ": " + what : what.charAt(0).toUpperCase() + what.slice(1);
    });
  }
  // check compares a complete snapshot with the stored one. The first snapshot after the switch
  // turns on is only stored. New items become pending sentences in the same step.
  function check() {
    if (!on()) {
      return Promise.resolve();
    }
    return locked(function () {
      var stored = state();
      return snapshot(stored, 3).then(function (snap) {
        if (!snap || !on()) {
          return;
        }
        var pending = stored ? stored.pending : [];
        if (stored) {
          pending = pending.concat(sentences(snap.items.filter(function (item) { return stored.seen.indexOf(key(item)) < 0; })));
        }
        set(STATE, { version: snap.version, seen: snap.items.map(key), pending: pending });
      });
    }).then(say, function () {}).catch(function () {});
  }
  function note(show) {
    var p = document.getElementById("speak-note");
    if (!p && show) {
      p = document.createElement("p");
      p.id = "speak-note";
      p.className = "notice";
      p.setAttribute("role", "status");
      p.textContent = "Click the page to hear the spoken alert.";
      list.parentNode.insertBefore(p, list.parentNode.firstChild);
    }
    if (p) {
      p.hidden = !show;
    }
  }
  function pendingNow() {
    var stored = state();
    return stored ? stored.pending : [];
  }
  // speakOne speaks a sentence and gives whether the browser finished it. A browser that
  // gives no end or error in 60 seconds counts as not finished: the attempt is cancelled, and
  // its late events change nothing.
  function speakOne(text) {
    return new Promise(function (resolve) {
      var done = false, guard;
      function finish(ok) {
        if (!done) {
          done = true;
          window.clearTimeout(guard);
          resolve(ok);
        }
      }
      var utterance = new window.SpeechSynthesisUtterance(text);
      utterance.onend = function () { finish(true); };
      utterance.onerror = function () { finish(false); };
      guard = window.setTimeout(function () {
        if (!done) {
          speech.cancel();
          finish(false);
        }
      }, 60000);
      speech.speak(utterance);
    });
  }
  // say speaks the pending sentences in order, one tab at a time. A sentence leaves the list
  // only when the browser finished it; one that the browser stopped, such as before the first
  // click on the page, stays for the next click.
  function say() {
    if (!on()) {
      return Promise.resolve();
    }
    return locked(function next() {
      var pending = pendingNow();
      if (!pending.length || !on()) {
        note(false);
        return undefined;
      }
      return speakOne(pending[0]).then(function (ok) {
        if (!ok) {
          note(true);
          return undefined;
        }
        var stored = state();
        if (stored && stored.pending[0] === pending[0]) {
          stored.pending = stored.pending.slice(1);
          set(STATE, stored);
        }
        return next();
      });
    });
  }
  document.addEventListener("click", function () {
    if (pendingNow().length) {
      say();
    }
  });
  // The Settings page's switch, test button and status.
  var box = document.getElementById("speak-switch"), test = document.getElementById("speak-test");
  var status = document.getElementById("speak-status");
  function limits() {
    if (!status || speech === undefined) {
      return;
    }
    var text = [];
    if (!shared) {
      text.push("This browser does not keep the switch: it lasts for this page only.");
    }
    if (!(navigator.locks && navigator.locks.request)) {
      text.push("Two dashboard tabs in this browser can both speak an alert.");
    }
    status.textContent = text.join(" ");
  }
  // Another tab that turns the switch off stops this tab's speech too.
  window.addEventListener("storage", function (event) {
    if (speech !== undefined && event.key === SWITCH && !on()) {
      speech.cancel();
      note(false);
      if (box) {
        box.checked = false;
      }
    }
  });
  if (box) {
    if (speech === undefined || window.SpeechSynthesisUtterance === undefined) {
      if (status) {
        status.textContent = "This browser cannot speak.";
      }
    } else {
      box.disabled = test.disabled = false;
      box.checked = on();
      limits();
      box.addEventListener("change", function () {
        // Off applies at once, also while another step holds the lock: the switch is stored
        // and the speech stops; other tabs stop at the storage event.
        if (!box.checked) {
          set(SWITCH, false);
          speech.cancel();
          note(false);
        }
        locked(function () {
          set(SWITCH, box.checked);
          set(STATE, undefined);
        }).then(check);
      });
      test.addEventListener("click", function () {
        speakOne("koinon: a decision waits for you");
      });
    }
  }
  check();
  window.setInterval(check, 10000);
})();
