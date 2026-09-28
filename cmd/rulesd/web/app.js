/* SETU — the applicant's console.
 *
 * Everything a person sees here is written for a person. No identifiers, no
 * rule expressions, no hashes. The engine's reasoning is still shown in full —
 * which questions decided an approval, and which Act says so — but in the
 * words the applicant was asked, not the language the rule was written in.
 *
 * The technical view of the same determination lives in `setuctl explain`.
 */
(function () {
  "use strict";

  var state = {
    schema: null, ruleset: null, scenarios: [],
    profile: {}, det: null, explain: {}, titles: {}, departments: [],
    view: "approvals", lang: "en", asOf: "", seq: 0, lastMs: 0
  };

  var $ = function (s, r) { return (r || document).querySelector(s); };
  function el(t, c, x) {
    var n = document.createElement(t);
    if (c) n.className = c;
    if (x !== undefined && x !== null) n.textContent = x;
    return n;
  }
  function clear(n) { while (n.firstChild) n.removeChild(n.firstChild); }
  function arr(x) { return x || []; }
  function rupee(s) { return (s || "").replace("INR ", "₹"); }
  function name(id) { return state.titles[id] || id; }
  function plural(n, one, many) { return n + " " + (n === 1 ? one : many); }

  function toast(msg) {
    var t = $("#toast");
    t.textContent = msg; t.hidden = false;
    clearTimeout(t._t);
    t._t = setTimeout(function () { t.hidden = true; }, 2400);
  }

  function api(path, body) {
    var o = { headers: { "Content-Type": "application/json" } };
    if (body !== undefined) { o.method = "POST"; o.body = JSON.stringify(body); }
    return fetch(path, o).then(function (r) {
      return r.json().then(function (j) {
        if (!r.ok) throw new Error(j.error || ("Something went wrong (" + r.status + ")"));
        return j;
      });
    });
  }

  /* ── Boot ───────────────────────────────────────────────────────── */
  function boot() {
    Promise.all([api("/api/ruleset"), api("/api/schema"), api("/api/scenarios")])
      .then(function (r) {
        state.ruleset = r[0]; state.schema = r[1]; state.scenarios = r[2].scenarios || [];
        state.asOf = state.ruleset.asOf;
        $("#place").textContent = placeName(state.ruleset);
        buildLanguages(); buildScenarioPicker(); buildForm(); buildViewTabs();
        if (state.scenarios.length) loadScenario(preferredScenario());
        else evaluate();
      })
      .catch(function (e) {
        clear($("#views"));
        $("#views").appendChild(el("p", "empty",
          "We could not reach the service. Is it still running? (" + e.message + ")"));
      });
  }

  /* The jurisdiction's own name, from the content's meta block. */
  function placeName(rs) {
    var m = rs.meta || {};
    if (m.title) return m.title.split("—")[0].trim();
    return rs.package;
  }

  function preferredScenario() {
    var keys = state.scenarios.map(function (s) { return s.key; });
    var pick = keys.filter(function (k) { return k.indexOf("coimbatore") >= 0; })[0];
    return pick || keys[0];
  }

  function buildLanguages() {
    var sel = $("#lang"), langs = state.schema.languages || ["en"];
    clear(sel);
    langs.forEach(function (l) { sel.appendChild(new Option(languageName(l), l)); });
    sel.value = state.lang;
    sel.addEventListener("change", function () {
      state.lang = sel.value; buildForm(); evaluate();
    });
    /* One language is not a choice; hide the control rather than offer a
       dropdown with a single entry. */
    $("#lang-wrap").hidden = langs.length < 2;
  }

  function languageName(code) {
    return ({ en: "English", ta: "தமிழ்", hi: "हिन्दी" })[code] || code;
  }

  function buildScenarioPicker() {
    var sel = $("#scenario");
    clear(sel);
    state.scenarios.forEach(function (s) {
      sel.appendChild(new Option(exampleLabel(s), s.key));
    });
    sel.addEventListener("change", function () { loadScenario(sel.value); });
    $("#btn-clear").addEventListener("click", function () {
      state.profile = {}; syncForm(); evaluate();
      toast("Cleared. Nothing is answered yet.");
    });
  }

  /* The example profiles are named by their scenario; show the part that
     distinguishes them rather than the file's name. */
  function exampleLabel(s) {
    return s.label.replace(/^Priya /, "").replace(/^Partial$/, "Half answered");
  }

  function loadScenario(key) {
    var s = state.scenarios.filter(function (x) { return x.key === key; })[0];
    if (!s) return;
    state.profile = {};
    Object.keys(s.profile).forEach(function (k) {
      if (k.charAt(0) !== "_") state.profile[k] = s.profile[k];
    });
    $("#scenario").value = key;
    syncForm(); evaluate();
  }

  /* ── The form, sectioned and generated from the encoded questions ── */
  function buildForm() {
    var form = $("#form");
    clear(form);
    var lastGroup = null;
    state.schema.variables.forEach(function (v) {
      var g = localised(v.group, "Other details");
      if (g !== lastGroup) {
        var head = el("div", "group-head");
        head.appendChild(el("span", null, g));
        head.appendChild(el("span", "group-count", ""));
        head.dataset.group = g;
        form.appendChild(head);
        lastGroup = g;
      }
      form.appendChild(field(v));
    });
    syncForm();
  }

  function localised(map, fallback) {
    if (!map) return fallback || "";
    return map[state.lang] || map.en || fallback || "";
  }

  function field(v) {
    var wrap = el("div", "fld" + (v.sensitive ? " sensitive" : ""));
    wrap.id = "fld-" + v.name;
    wrap.dataset.group = localised(v.group, "Other details");

    var q = el("label", "fld-q", localised(v.question, v.name));
    q.setAttribute("for", "in-" + v.name);
    wrap.appendChild(q);

    var hint = localised(v.hint, "");
    if (hint) wrap.appendChild(el("p", "fld-hint", hint));

    var row = el("div", "fld-row");
    row.appendChild(widget(v));
    wrap.appendChild(row);

    /* Why the engine is waiting on this one, filled in after each evaluation. */
    wrap.appendChild(el("p", "fld-why", ""));

    var foot = el("div", "fld-top");
    foot.style.marginTop = "7px";
    foot.appendChild(el("span", "fld-state", ""));
    var clr = el("button", "fld-clear", "clear");
    clr.type = "button";
    clr.addEventListener("click", function () {
      delete state.profile[v.name]; syncForm(); evaluate();
    });
    foot.appendChild(clr);
    wrap.appendChild(foot);
    return wrap;
  }

  function widget(v) {
    var host, input;
    switch (v.widget) {
      case "bool":
        host = el("div", "seg");
        [["Yes", true], ["No", false]].forEach(function (pair) {
          var b = el("button", null, pair[0]);
          b.type = "button";
          b.dataset.val = String(pair[1]);
          b.addEventListener("click", function () {
            state.profile[v.name] = pair[1]; syncForm(); evaluate();
          });
          host.appendChild(b);
        });
        return host;

      case "enum":
        input = el("select");
        input.id = "in-" + v.name;
        input.appendChild(new Option("Not answered yet", ""));
        (state.schema.enums[v.enumType] || []).forEach(function (m) {
          input.appendChild(new Option(m.title || sentence(m.name), m.name));
        });
        input.addEventListener("change", function () {
          if (!input.value) delete state.profile[v.name];
          else state.profile[v.name] = input.value;
          syncForm(); evaluate();
        });
        return input;

      case "geo":
        host = el("div", "geo-row");
        host.style.flex = "1";
        [["lat", "Latitude"], ["lon", "Longitude"]].forEach(function (p) {
          var n = el("input");
          n.type = "number"; n.step = "0.0001"; n.placeholder = p[1];
          n.id = "in-" + v.name + "-" + p[0];
          n.addEventListener("input", function () {
            var a = $("#in-" + v.name + "-lat").value, b = $("#in-" + v.name + "-lon").value;
            if (a === "" || b === "") delete state.profile[v.name];
            else state.profile[v.name] = [parseFloat(a), parseFloat(b)];
            debounced();
          });
          host.appendChild(n);
        });
        return host;

      case "date":
        input = el("input");
        input.type = "date"; input.id = "in-" + v.name;
        input.addEventListener("change", function () {
          if (!input.value) delete state.profile[v.name];
          else state.profile[v.name] = input.value;
          syncForm(); evaluate();
        });
        return input;

      case "money":
      case "quantity":
        host = el("div", "fld-row");
        host.style.flex = "1";
        input = el("input");
        input.type = "number"; input.id = "in-" + v.name;
        input.placeholder = v.example || "";
        input.addEventListener("input", function () {
          if (input.value === "") delete state.profile[v.name];
          else state.profile[v.name] = parseFloat(input.value);
          debounced();
        });
        host.appendChild(input);
        /* The unit is shown because the number is read in it. */
        host.appendChild(el("span", "unit",
          v.widget === "money" ? "₹" : (v.unitLabel || v.unit || "")));
        return host;

      default:
        input = el("input");
        input.type = "text"; input.id = "in-" + v.name;
        input.addEventListener("input", function () {
          if (input.value === "") delete state.profile[v.name];
          else state.profile[v.name] = input.value;
          debounced();
        });
        return input;
    }
  }

  /* SPICE_GRINDING -> Spice grinding, for any enum with no authored title. */
  function sentence(s) {
    var t = s.toLowerCase().replace(/_/g, " ");
    return t.charAt(0).toUpperCase() + t.slice(1);
  }

  function syncForm() {
    var answered = 0;
    var perGroup = {};
    state.schema.variables.forEach(function (v) {
      var has = Object.prototype.hasOwnProperty.call(state.profile, v.name);
      if (has) answered++;
      var g = localised(v.group, "Other details");
      perGroup[g] = perGroup[g] || { a: 0, t: 0 };
      perGroup[g].t++;
      if (has) perGroup[g].a++;

      var wrap = document.getElementById("fld-" + v.name);
      if (!wrap) return;
      wrap.classList.toggle("answered", has);
      var st = wrap.querySelector(".fld-state");
      if (st) st.textContent = has ? "Answered" : "Not answered";

      var val = state.profile[v.name];
      if (v.widget === "bool") {
        Array.prototype.forEach.call(wrap.querySelectorAll(".seg button"), function (b) {
          b.setAttribute("aria-pressed", has && String(val) === b.dataset.val ? "true" : "false");
        });
      } else if (v.widget === "geo") {
        var la = document.getElementById("in-" + v.name + "-lat");
        var lo = document.getElementById("in-" + v.name + "-lon");
        if (la && lo) { la.value = has && val ? val[0] : ""; lo.value = has && val ? val[1] : ""; }
      } else {
        var input = document.getElementById("in-" + v.name);
        if (input) input.value = has ? val : "";
      }
    });

    Array.prototype.forEach.call(document.querySelectorAll(".group-head"), function (h) {
      var g = perGroup[h.dataset.group];
      if (g) h.querySelector(".group-count").textContent = g.a + " of " + g.t;
    });

    var total = state.schema.variables.length;
    $("#progress-fill").style.width = (total ? answered * 100 / total : 0) + "%";
    $("#progress-txt").textContent = answered + " of " + total + " answered";
  }

  /* Highlight, in the form, the questions the engine is actually waiting on. */
  function markNeeded() {
    var needed = {};
    arr(state.det && state.det.unresolved).forEach(function (u) {
      var affects = arr(u.affectsApprovals).map(name);
      needed[u.var] = affects.length
        ? "Needed to decide: " + affects.join(", ")
        : "Needed to complete your answer";
    });
    state.schema.variables.forEach(function (v) {
      var wrap = document.getElementById("fld-" + v.name);
      if (!wrap) return;
      var why = wrap.querySelector(".fld-why");
      if (needed[v.name]) {
        wrap.classList.add("needed");
        why.textContent = needed[v.name];
      } else {
        wrap.classList.remove("needed");
        why.textContent = "";
      }
    });
  }

  var timer;
  function debounced() {
    clearTimeout(timer);
    timer = setTimeout(function () { syncForm(); evaluate(); }, 260);
  }

  /* ── Evaluate ───────────────────────────────────────────────────── */
  function evaluate() {
    var seq = ++state.seq, t0 = performance.now();
    api("/api/evaluate", {
      profile: state.profile, asOf: state.asOf, language: state.lang, trace: false
    }).then(function (r) {
      if (seq !== state.seq) return;
      state.det = r.determination;
      state.explain = r.explanations || {};
      state.titles = r.titles || {};
      state.departments = r.departments || [];
      state.lastMs = Math.round(performance.now() - t0);
      renderStats(); renderViewTabs(); renderView(); markNeeded(); renderAbout();
    }).catch(function (e) {
      if (seq !== state.seq) return;
      clear($("#views"));
      $("#views").appendChild(el("p", "empty", e.message));
    });
  }

  function renderStats() {
    var d = state.det, host = $("#stats");
    clear(host);
    var saved = d.dag.sequentialDays - d.dag.totalDays;
    var pct = d.dag.sequentialDays ? Math.round(saved * 100 / d.dag.sequentialDays) : 0;
    var open = arr(d.unresolved).length;
    var worth = arr(d.incentives).reduce(function (a, m) {
      var n = parseInt((m.value || "").replace(/[^0-9]/g, ""), 10);
      return a + (isNaN(n) ? 0 : n);
    }, 0);

    [{ k: "Approvals you need", v: String(arr(d.approvals).length),
       n: plural(state.departments.length, "department", "departments") },
     { k: "Government fees", v: rupee(d.feeTotal),
       n: d.feeComplete ? "in total" : "so far", c: "accent" },
     { k: "Time if filed together", v: d.dag.totalDays + " days",
       n: pct ? pct + "% faster than one at a time" : "", c: "accent" },
     open
       ? { k: "Questions left", v: String(open), n: "answers still needed", c: "amber" }
       : { k: "You could claim", v: worth ? "₹" + indian(worth) : "—",
           n: plural(arr(d.incentives).length, "scheme", "schemes"), c: "accent" }
    ].forEach(function (c) {
      var b = el("div", "stat");
      b.appendChild(el("div", "stat-k", c.k));
      b.appendChild(el("div", "stat-v " + (c.c || ""), c.v));
      b.appendChild(el("div", "stat-n", c.n));
      host.appendChild(b);
    });
  }

  /* Indian digit grouping: 46,00,000 rather than 4,600,000. */
  function indian(n) {
    var s = String(n);
    if (s.length <= 3) return s;
    var head = s.slice(0, -3), tail = s.slice(-3), parts = [];
    while (head.length > 2) { parts.unshift(head.slice(-2)); head = head.slice(0, -2); }
    if (head) parts.unshift(head);
    return parts.join(",") + "," + tail;
  }

  /* ── Views ──────────────────────────────────────────────────────── */
  var VIEWS = [
    { id: "approvals",   label: "What you need" },
    { id: "unresolved",  label: "Questions left", warn: true },
    { id: "timeline",    label: "In what order" },
    { id: "departments", label: "Who you deal with" },
    { id: "fees",        label: "What it costs" },
    { id: "documents",   label: "Papers to gather" },
    { id: "incentives",  label: "Money you can claim" },
    { id: "obligations", label: "After you open" }
  ];

  function buildViewTabs() {
    var host = $("#viewtabs");
    clear(host);
    VIEWS.forEach(function (v) {
      var b = el("button", "viewtab" + (v.warn ? " warn" : ""));
      b.setAttribute("role", "tab");
      b.dataset.view = v.id;
      b.appendChild(document.createTextNode(v.label));
      b.appendChild(el("span", "count", ""));
      b.addEventListener("click", function () {
        state.view = v.id; renderViewTabs(); renderView();
      });
      host.appendChild(b);
    });
  }

  function renderViewTabs() {
    var d = state.det;
    Array.prototype.forEach.call($("#viewtabs").children, function (b) {
      var id = b.dataset.view, n = "";
      if (d) {
        if (id === "approvals") n = arr(d.approvals).length;
        else if (id === "unresolved") n = arr(d.unresolved).length + arr(d.conflicts).length;
        else if (id === "fees") n = arr(d.fees).length;
        else if (id === "documents") n = arr(d.documents).length;
        else if (id === "incentives") n = arr(d.incentives).length;
        else if (id === "obligations") n = arr(d.obligations).length;
        else if (id === "departments") n = state.departments.length;
      }
      b.querySelector(".count").textContent = n === "" ? "" : String(n);
      b.setAttribute("aria-selected", id === state.view ? "true" : "false");
      /* A tab with nothing behind it is a dead end; hide it. */
      b.hidden = (n === 0 && id !== "approvals");
    });
  }

  function renderView() {
    var host = $("#views");
    clear(host);
    if (!state.det) return;
    ({
      approvals: viewApprovals, unresolved: viewUnresolved, timeline: viewTimeline,
      departments: viewDepartments, fees: viewFees, documents: viewDocuments,
      incentives: viewIncentives, obligations: viewObligations
    })[state.view](host, state.det);
  }

  /* What you need */
  function viewApprovals(host, d) {
    if (!arr(d.approvals).length) {
      host.appendChild(el("p", "empty",
        "Nothing is required yet on what you have told us. Answer a few more " +
        "questions on the left and this will fill in."));
      return;
    }
    host.appendChild(el("p", "note",
      "Open any one to see why it applies to you and which law says so."));

    var rows = el("div", "rows");
    d.approvals.forEach(function (a, i) {
      var row = el("div", "row");
      var btn = el("button", "row-btn");
      btn.type = "button";
      btn.setAttribute("aria-expanded", "false");
      btn.appendChild(el("span", "row-n", String(i + 1)));

      var mid = el("span");
      mid.appendChild(el("span", "row-title", a.title));
      mid.appendChild(el("span", "row-id", a.departmentName || name(a.department)));
      btn.appendChild(mid);

      var meta = el("span", "row-meta");
      meta.appendChild(el("span", null, "takes up to " + a.sla));
      meta.appendChild(el("span", "chev", "▸"));
      btn.appendChild(meta);
      row.appendChild(btn);

      var body = el("div", "row-body");
      body.hidden = true;

      if (a.reason) body.appendChild(el("p", "why", a.reason));

      var ex = state.explain[a.id] || {};
      if (arr(ex.because).length) {
        var box = el("div", "because");
        box.appendChild(el("div", "because-h", "Because you told us"));
        var ul = el("ul", "because-list");
        ex.because.forEach(function (b) {
          var li = el("li");
          li.appendChild(el("span", "because-q", b.question));
          li.appendChild(el("span", "because-a", b.answer));
          ul.appendChild(li);
        });
        box.appendChild(ul);
        body.appendChild(box);
      } else if (ex.prerequisite) {
        body.appendChild(el("p", "why",
          "You need this before you can be granted your " + ex.prerequisite + "."));
      }

      if (ex.act) {
        var law = el("p", "lawline");
        law.appendChild(document.createTextNode("This comes from "));
        law.appendChild(el("b", null, ex.act));
        if (ex.provision) law.appendChild(document.createTextNode(", " + ex.provision));
        law.appendChild(document.createTextNode("."));
        body.appendChild(law);
      }

      var dl = el("dl", "kv");
      function kv(k, v) {
        if (!v) return;
        dl.appendChild(el("dt", null, k));
        var dd = el("dd");
        if (v instanceof Node) dd.appendChild(v); else dd.textContent = v;
        dl.appendChild(dd);
      }
      kv("Who decides", a.departmentName || name(a.department));
      kv("How long", "Up to " + a.sla + " by law");
      if (a.validity) {
        kv("Then valid for", a.validity +
          (a.renewalIn ? " — start renewing " + a.renewalIn + " before it expires" : ""));
      }
      if (arr(a.dependsOn).length) {
        var f = el("span");
        a.dependsOn.forEach(function (dep, j) {
          if (j) f.appendChild(el("br"));
          f.appendChild(document.createTextNode(dep.title +
            (dep.reason ? " — " + dep.reason : "")));
        });
        kv("Get this first", f);
      }
      if (arr(a.documents).length) kv("Papers needed", a.documents.map(name).join(", "));
      if (arr(a.fees).length) {
        var total = arr(d.fees).filter(function (x) { return x.approvalId === a.id; })
          .map(function (x) { return x.computed ? rupee(x.amount) : "not yet known"; });
        kv("Fee", total.join(", "));
      }
      if (a.appealAuthority) {
        kv("If refused", "You may appeal to the " + a.appealAuthority +
          " within " + a.appealWithin + ".");
      }
      if (a.deemedApprovalPermitted) {
        kv("If they are late", "The law treats this as granted if the department " +
          "does not decide within the time above.");
      }
      body.appendChild(dl);

      row.appendChild(body);
      btn.addEventListener("click", function () {
        var open = body.hidden;
        body.hidden = !open;
        row.classList.toggle("open", open);
        btn.setAttribute("aria-expanded", open ? "true" : "false");
      });
      rows.appendChild(row);
    });
    host.appendChild(rows);
  }

  /* Questions left */
  function viewUnresolved(host, d) {
    arr(d.conflicts).forEach(function (c) {
      var box = el("div", "conflict");
      box.appendChild(el("h3", null, "We need a person to look at this"));
      box.appendChild(el("p", null,
        "Two provisions of equal standing disagree about whether you need your " +
        name(c.approvalId) + ". We will not guess. This has been flagged for the " +
        "department to clarify, and you will be told the outcome."));
      host.appendChild(box);
    });

    if (!arr(d.unresolved).length) {
      if (!arr(d.conflicts).length) {
        host.appendChild(el("p", "empty",
          "Nothing outstanding — everything is settled on what you have told us."));
      }
      return;
    }

    var box = el("div", "unknowns");
    var head = el("div", "unknowns-head");
    head.appendChild(el("p", "unknowns-title",
      plural(d.unresolved.length, "question", "questions") + " left to answer"));
    head.appendChild(el("p", "unknowns-sub",
      "We only ask what actually changes your answer. Anything we have not asked " +
      "about cannot affect what you need."));
    box.appendChild(head);

    d.unresolved.forEach(function (u) {
      var q = el("div", "q");
      q.appendChild(el("p", "q-text", u.question));
      if (u.hint) q.appendChild(el("p", "q-hint", u.hint));
      var affects = arr(u.affectsApprovals).map(name)
        .concat(arr(u.affectsFees).map(name), arr(u.affectsIncentives).map(name));
      if (affects.length) {
        var det = el("div", "q-det");
        det.appendChild(document.createTextNode("Until you answer, we cannot tell you about "));
        det.appendChild(el("b", null, affects.join(", ")));
        q.appendChild(det);
      }
      var jump = el("button", "q-jump", "Answer this");
      jump.type = "button";
      jump.addEventListener("click", function () {
        var f = document.getElementById("fld-" + u.var);
        if (!f) return;
        f.scrollIntoView({ behavior: "smooth", block: "center" });
        var i = f.querySelector("input, select, .seg button");
        if (i) i.focus();
      });
      q.appendChild(jump);
      box.appendChild(q);
    });
    host.appendChild(box);
  }

  /* In what order — a plan, not a Gantt axis */
  function viewTimeline(host, d) {
    if (!arr(d.dag.waves).length) {
      host.appendChild(el("p", "empty", "Nothing to plan yet."));
      return;
    }
    host.appendChild(el("p", "note",
      "Most of these can be filed on the same day. Only a few have to wait for " +
      "another to be granted first — that is what makes the difference between " +
      d.dag.totalDays + " days and " + d.dag.sequentialDays + "."));

    var byId = {};
    arr(d.dag.nodes).forEach(function (n) { byId[n.id] = n; });

    var wrap = el("div");
    d.dag.waves.forEach(function (wave, wi) {
      var step = el("div", "step");
      var mark = el("div", "step-mark");
      mark.appendChild(el("div", "step-dot"));
      mark.appendChild(el("div", "step-line"));
      step.appendChild(mark);

      var body = el("div", "step-body");
      var slowest = wave.map(function (id) { return byId[id]; })
        .sort(function (a, b) { return b.finishDay - a.finishDay; })[0];

      body.appendChild(el("div", "step-when",
        wi === 0 ? "Start now" : "After the step above is granted"));
      body.appendChild(el("h3", "step-h",
        wi === 0
          ? "File these " + wave.length + " together"
          : (wave.length === 1 ? "Then this one" : "Then these " + wave.length)));
      body.appendChild(el("p", "step-p",
        "Longest of them: " + slowest.title + ", up to " + slowest.slaDays +
        " days. This step is done around day " + slowest.finishDay + "."));

      var list = el("div", "rows");
      wave.map(function (id) { return byId[id]; })
        .sort(function (a, b) { return b.slaDays - a.slaDays; })
        .forEach(function (n) {
          var r = el("div", "row");
          var b = el("div", "row-btn");
          b.style.cursor = "default";
          b.appendChild(el("span", "row-n", ""));
          var m = el("span");
          m.appendChild(el("span", "row-title", n.title));
          b.appendChild(m);
          var meta = el("span", "row-meta");
          meta.appendChild(el("span", null, "up to " + n.slaDays + " days"));
          if (n.critical) meta.appendChild(el("span", "pill req", "sets the pace"));
          b.appendChild(meta);
          r.appendChild(b);
          list.appendChild(r);
        });
      body.appendChild(list);
      step.appendChild(body);
      wrap.appendChild(step);
    });
    host.appendChild(wrap);
  }

  /* Who you deal with */
  function viewDepartments(host) {
    host.appendChild(el("p", "note",
      "You will not have to visit any of them separately — this is who is " +
      "deciding what, so you know where each answer comes from."));
    var wrap = el("div", "depts");
    state.departments.forEach(function (dp) {
      var box = el("div", "dept");
      box.appendChild(el("p", "dept-n", dp.name));
      box.appendChild(el("p", "dept-l", dp.approvals.join(" · ")));
      wrap.appendChild(box);
    });
    host.appendChild(wrap);
  }

  /* Tables */
  function table(cols, rows, footRow) {
    var wrap = el("div", "tbl-wrap"), tbl = el("table");
    var thead = el("thead"), htr = el("tr");
    cols.forEach(function (c) {
      var th = el("th", null, c.label);
      if (c.num) th.style.textAlign = "right";
      htr.appendChild(th);
    });
    thead.appendChild(htr); tbl.appendChild(thead);
    var tb = el("tbody");
    rows.forEach(function (r) {
      var tr = el("tr");
      r.forEach(function (cell, i) {
        var td = el("td", cols[i].num ? "num" : "");
        if (cell instanceof Node) td.appendChild(cell); else td.textContent = cell;
        tr.appendChild(td);
      });
      tb.appendChild(tr);
    });
    if (footRow) {
      var f = el("tr", "total");
      footRow.forEach(function (c, i) { f.appendChild(el("td", cols[i].num ? "num" : "", c)); });
      tb.appendChild(f);
    }
    tbl.appendChild(tb); wrap.appendChild(tbl);
    return wrap;
  }

  function titled(main, sub) {
    var s = el("span");
    s.appendChild(document.createTextNode(main));
    if (sub) s.appendChild(el("span", "sub", sub));
    return s;
  }

  function viewFees(host, d) {
    host.appendChild(el("p", "note",
      "You pay this once, to us. We split it between the departments — you do " +
      "not pay each of them separately."));
    host.appendChild(table(
      [{ label: "What for" }, { label: "Amount", num: true }],
      d.fees.map(function (f) {
        return [titled(f.title, "for your " + name(f.approvalId)),
                f.computed ? rupee(f.amount) : "not yet known"];
      }),
      ["Total to pay", rupee(d.feeTotal) + (d.feeComplete ? "" : " so far")]));
  }

  function viewDocuments(host, d) {
    host.appendChild(el("p", "note",
      "Each of these is needed once, however many approvals ask for it. Give it " +
      "to us once and we pass it on."));
    host.appendChild(table(
      [{ label: "Paper" }, { label: "How it is checked" }, { label: "Used for", num: true }],
      d.documents.map(function (doc) {
        return [titled(doc.title, doc.issuer ? "issued by " + doc.issuer : ""),
                verificationWords(doc),
                plural(arr(doc.requiredFor).length, "approval", "approvals")];
      })));
  }

  function verificationWords(doc) {
    switch (doc.verification) {
      case "api": return "We check it automatically";
      case "digilocker": return "Pulled from your DigiLocker";
      case "officer": return "An officer reviews it";
      default: return "You declare it yourself";
    }
  }

  function viewIncentives(host, d) {
    host.appendChild(el("p", "note",
      "You appear to qualify for these. We found them from the same answers — " +
      "you did not have to go looking."));
    host.appendChild(table(
      [{ label: "Scheme" }, { label: "Apply before" }, { label: "Worth up to", num: true }],
      d.incentives.map(function (m) {
        return [titled(m.title, m.reason || m.scheme),
                m.windowTo ? friendlyDate(m.windowTo) : "—",
                m.computed ? rupee(m.value) : "—"];
      })));
  }

  function viewObligations(host, d) {
    host.appendChild(el("p", "note",
      "Once you are open, these keep coming round. We will remind you before " +
      "each one is due."));
    host.appendChild(table(
      [{ label: "What" }, { label: "How often" }, { label: "If you miss it" }],
      d.obligations.map(function (o) {
        var when = o.cadence || "—";
        if (o.dueMonth) when += ", by " + o.dueDay + " " + monthName(o.dueMonth);
        else if (o.dueWithin) when += ", within " + o.dueWithin;
        return [titled(o.title, "for your " + name(o.approvalId)), when, o.consequence || "—"];
      })));
  }

  function monthName(m) {
    return ["", "January", "February", "March", "April", "May", "June", "July",
            "August", "September", "October", "November", "December"][m] || "";
  }
  function friendlyDate(iso) {
    var p = String(iso).split("-");
    if (p.length !== 3) return iso;
    return parseInt(p[2], 10) + " " + monthName(parseInt(p[1], 10)) + " " + p[0];
  }

  /* ── About this answer ──────────────────────────────────────────── */
  function renderAbout() {
    var host = $("#about"), d = state.det;
    clear(host);

    function p(text, cls) { host.appendChild(el("p", cls || null, text)); }

    p("This answer was worked out from the law as it stood on " +
      friendlyDate(d.asOf.Year + "-" + String(d.asOf.Month).padStart(2, "0") + "-" +
        String(d.asOf.Day).padStart(2, "0")) +
      ", in " + state.lastMs + " milliseconds. Nothing here was written by a " +
      "person for you: every line was worked out from your answers and the " +
      "rules currently in force.");

    p("If the same answers are entered again, the result is identical, and it " +
      "can be reproduced years from now — which is what makes it something you " +
      "could rely on in a dispute.");

    var warn = el("p", "warn");
    warn.appendChild(el("b", null, "This is a demonstration. "));
    warn.appendChild(document.createTextNode(
      "The rules behind it were written from public sources and have not been " +
      "checked by a lawyer or approved by any department. Do not act on it. " +
      "The system itself refuses to publish these rules for real use until they " +
      "have been reviewed and signed off."));
    host.appendChild(warn);
  }

  /* ── Theme ──────────────────────────────────────────────────────── */
  $("#btn-theme").addEventListener("click", function () {
    var root = document.documentElement, cur = root.getAttribute("data-theme");
    var next = cur === "dark" ? "light" : (cur === "light" ? null : "dark");
    if (next) root.setAttribute("data-theme", next);
    else root.removeAttribute("data-theme");
    try { localStorage.setItem("setu-theme", next || ""); } catch (e) { /* private mode */ }
  });
  try {
    var saved = localStorage.getItem("setu-theme");
    if (saved) document.documentElement.setAttribute("data-theme", saved);
  } catch (e) { /* private mode */ }

  boot();
})();
