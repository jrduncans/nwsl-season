document.addEventListener("change", (event) => {
  const season = event.target.closest("[data-season-selector]");
  if (season) {
    const switcher = season.closest("[data-season-switcher]");
    const destinations = Array.from(switcher?.querySelectorAll("a[data-season-destination]") ?? []);
    const destination = destinations.find((link) => link.dataset.seasonDestination === season.value);
    if (destination) destination.click();
    return;
  }

  const form = event.target.closest("form[data-auto-submit]");
  if (!form || event.target.tagName !== "SELECT") return;
  form.requestSubmit();
});

document.addEventListener("click", (event) => {
  const button = event.target.closest("[data-standings-mode-button]");
  if (!button) return;

  const display = button.closest("[data-standings]");
  const mode = button.dataset.standingsModeValue;
  if (!display || !["per-game", "total"].includes(mode) || display.dataset.standingsMode === mode) return;

  display.classList.add("is-switching");
  requestAnimationFrame(() => {
    display.dataset.standingsMode = mode;
    sortStandings(display, mode, display.dataset.standingsStat);
    display.querySelectorAll("[data-standings-mode-button]").forEach((candidate) => {
      candidate.setAttribute("aria-pressed", String(candidate === button));
    });
    updateStandingsValues(display, mode);
    requestAnimationFrame(() => display.classList.remove("is-switching"));
  });
});

document.addEventListener("click", (event) => {
  const button = event.target.closest("[data-standings-stat-button]");
  if (!button) return;

  const display = button.closest("[data-standings]");
  const stat = button.dataset.standingsStatValue;
  if (!display || !["goals", "xg"].includes(stat) || display.dataset.standingsStat === stat) return;

  display.dataset.standingsStat = stat;
  display.querySelectorAll("[data-standings-stat-button]").forEach((candidate) => {
    candidate.setAttribute("aria-pressed", String(candidate === button));
  });
  display.querySelectorAll("[data-standings-points-label]").forEach((label) => {
    label.textContent = stat === "xg" ? label.dataset.xgLabel : label.dataset.goalsLabel;
    label.title = stat === "xg" ? "Expected points from ASA's game xG model" : "Official standings points";
  });
  display.querySelectorAll("[data-standings-caption]").forEach((caption) => {
    caption.textContent = stat === "xg" ? caption.dataset.xgLabel : caption.dataset.goalsLabel;
  });
  updateStandingsValues(display, display.dataset.standingsMode);
  sortStandings(display, display.dataset.standingsMode, stat);
});

function updateStandingsValues(display, mode) {
  const stat = display.dataset.standingsStat;
  display.querySelectorAll("[data-standings-value]").forEach((value) => {
    const cell = value.parentElement;
    const key = cell.hasAttribute("data-standings-points") && stat === "xg"
      ? mode === "per-game" ? "xgPerGame" : "xgTotal"
      : mode === "per-game" ? "perGame" : "total";
    value.textContent = cell.dataset[key];
  });
}

function formatLocalTime(utc) {
  const date = new Date(utc);
  if (Number.isNaN(date.getTime())) return null;
  const datePart = new Intl.DateTimeFormat(undefined, {
    weekday: "short",
    month: "short",
    day: "numeric",
  }).format(date);
  const timePart = new Intl.DateTimeFormat(undefined, {
    hour: "numeric",
    minute: "2-digit",
    timeZoneName: "short",
  }).format(date);
  return `${datePart} at ${timePart}`;
}

function localizeTimes() {
  document.querySelectorAll("[data-local-time]").forEach((element) => {
    if (element.tagName === "OPTION") return;
    const localTime = formatLocalTime(element.dataset.localTime);
    if (!localTime) return;
    element.textContent = localTime;
  });
}

function setupEvaluationCharts() {
  document.querySelectorAll("[data-evaluation-chart]").forEach((panel) => {
    let data;
    try {
      data = JSON.parse(panel.dataset.evaluation);
    } catch {
      return;
    }
    const metricControl = panel.querySelector("[data-evaluation-metric]");
    const scaleControl = panel.querySelector("[data-evaluation-scale]");
    const windowControl = panel.querySelector("[data-evaluation-window]");
    const svg = panel.querySelector("[data-evaluation-svg]");
    const summary = panel.querySelector("[data-evaluation-summary]");
    const legend = panel.querySelector("[data-evaluation-legend]");
    const baseline = data.models.find((model) => model.id === data.baseline_id);
    if (!metricControl || !scaleControl || !windowControl || !svg || !summary || !legend || !baseline) {
      panel.replaceChildren(Object.assign(document.createElement("p"), { className: "data-warning", textContent: "The simple baseline is not available in this evaluation artifact." }));
      return;
    }

    const colors = ["#6b7280", "#c26a00", "#2563eb", "#15803d", "#7c3aed"];
    const colorFor = (id) => colors[Math.max(0, data.models.findIndex((model) => model.id === id)) % colors.length];
    const element = (name, attributes = {}) => {
      const value = document.createElementNS("http://www.w3.org/2000/svg", name);
      Object.entries(attributes).forEach(([key, attribute]) => value.setAttribute(key, String(attribute)));
      return value;
    };
    const formatNumber = (value, precision = 2) => Number(value).toFixed(precision);

    const render = () => {
      const metric = metricControl.value;
      const scale = scaleControl.value;
      const windowName = windowControl.value;
      const field = metric === "points" ? "points_mae" : "position_mae";
      const baselineStages = new Map((baseline.windows[windowName]?.stages ?? []).map((stage) => [stage.label, stage]));
      const lines = data.models.map((model) => {
        const stages = (model.windows[windowName]?.stages ?? []).flatMap((stage) => {
          const base = baselineStages.get(stage.label);
          if (!base || !base[field]) return [];
          return [{ ...stage, value: scale === "relative" ? stage[field] / base[field] : stage[field] }];
        });
        return { ...model, stages };
      }).filter((model) => model.stages.length > 0);
      const values = lines.flatMap((model) => model.stages.map((stage) => stage.value));
      if (values.length === 0) {
        summary.textContent = "No stage-level results are available for this view.";
        svg.replaceChildren();
        legend.replaceChildren();
        return;
      }
      const width = 760;
      const height = 340;
      const margin = { top: 24, right: 24, bottom: 54, left: 62 };
      const xValues = [...new Set(lines.flatMap((model) => model.stages.map((stage) => stage.progress)))].sort((a, b) => a - b);
      let min = Math.min(...values);
      let max = Math.max(...values);
      if (scale === "relative") {
        min = Math.min(min, 1);
        max = Math.max(max, 1);
      }
      const padding = Math.max((max - min) * .16, scale === "relative" ? .025 : .1);
      min = Math.max(0, min - padding);
      max += padding;
      const x = (value) => margin.left + ((value - xValues[0]) / Math.max(1, xValues[xValues.length - 1] - xValues[0])) * (width - margin.left - margin.right);
      const y = (value) => height - margin.bottom - ((value - min) / Math.max(.0001, max - min)) * (height - margin.top - margin.bottom);

      const nodes = [element("title"), element("desc")];
      nodes[0].textContent = metric === "points" ? "Final-points forecast error through the season" : "Final table-position forecast error through the season";
      nodes[1].textContent = scale === "relative" ? "Values below one have less error than the simple baseline." : "Lower values mean closer forecasts.";
      for (let i = 0; i <= 4; i++) {
        const value = min + (max - min) * i / 4;
        nodes.push(element("line", { x1: margin.left, x2: width - margin.right, y1: y(value), y2: y(value), class: "evaluation-grid" }));
        const label = element("text", { x: margin.left - 9, y: y(value) + 4, "text-anchor": "end", class: "evaluation-axis-label" });
        label.textContent = scale === "relative" ? `${formatNumber(value)}×` : formatNumber(value, 1);
        nodes.push(label);
      }
      xValues.forEach((value) => {
        nodes.push(element("line", { x1: x(value), x2: x(value), y1: height - margin.bottom, y2: height - margin.bottom + 5, class: "evaluation-axis" }));
        const label = element("text", { x: x(value), y: height - margin.bottom + 23, "text-anchor": "middle", class: "evaluation-axis-label" });
        label.textContent = `${value}%`;
        nodes.push(label);
      });
      const axisTitle = element("text", { x: (margin.left + width - margin.right) / 2, y: height - 9, "text-anchor": "middle", class: "evaluation-axis-title" });
      axisTitle.textContent = "Regular season complete";
      nodes.push(axisTitle);
      if (scale === "relative" && min <= 1 && max >= 1) {
        nodes.push(element("line", { x1: margin.left, x2: width - margin.right, y1: y(1), y2: y(1), class: "evaluation-baseline-line" }));
      }
      lines.forEach((model) => {
        const path = element("path", { d: model.stages.map((stage, index) => `${index === 0 ? "M" : "L"}${x(stage.progress)},${y(stage.value)}`).join(" "), fill: "none", stroke: colorFor(model.id), "stroke-width": 3, class: model.id === data.baseline_id ? "evaluation-baseline-path" : "" });
        nodes.push(path);
        model.stages.forEach((stage) => nodes.push(element("circle", { cx: x(stage.progress), cy: y(stage.value), r: 4, fill: colorFor(model.id) })));
      });
      svg.setAttribute("viewBox", `0 0 ${width} ${height}`);
      svg.setAttribute("aria-label", `${nodes[0].textContent}. ${nodes[1].textContent}`);
      svg.replaceChildren(...nodes);

      legend.replaceChildren(...lines.map((model) => {
        const item = document.createElement("span");
        const marker = document.createElement("i");
        marker.style.background = colorFor(model.id);
        item.append(marker, model.name);
        return item;
      }));
      const unit = metric === "points" ? "points" : "places";
      summary.textContent = scale === "relative"
        ? "The simple straight-line pace baseline is fixed at 1.00. Lower lines are closer to the final table."
        : `Lower is better: this is the average final-table error per team, measured in ${unit}.`;
    };
    [metricControl, scaleControl, windowControl].forEach((control) => control.addEventListener("change", render));
    render();
  });
}

setupEvaluationCharts();

function updateForecastOutcomeLabels() {
  const fixture = document.querySelector("#forecast-fixture");
  const selected = fixture?.selectedOptions[0];
  if (!selected) return;
  const home = document.querySelector('[data-forecast-outcome="h"]');
  const away = document.querySelector('[data-forecast-outcome="a"]');
  if (home) home.textContent = `${selected.dataset.homeTeam} win`;
  if (away) away.textContent = `${selected.dataset.awayTeam} win`;
}

function formatForecastFixtureLabel(option, teamID) {
  const localTime = formatLocalTime(option.dataset.localTime) ?? option.dataset.kickoffFallback;
  let label = option.dataset.fixtureLabel;
  if (teamID === option.dataset.homeTeamId) label = option.dataset.homeLabel;
  if (teamID === option.dataset.awayTeamId) label = option.dataset.awayLabel;
  option.textContent = `${localTime} · ${label}`;
}

function forecastOutcomeLabel(option, outcome) {
  if (outcome === "h") return `${option.dataset.homeTeam} win`;
  if (outcome === "a") return `${option.dataset.awayTeam} win`;
  return "Draw";
}

function setupForecastAssumptionBuilder() {
  const builder = document.querySelector("form[data-assumption-builder]");
  const team = document.querySelector("#forecast-team");
  const fixture = document.querySelector("#forecast-fixture");
  const outcomes = Array.from(builder?.querySelectorAll('input[name="outcome"]') ?? []);
  const addButton = builder?.querySelector('button[type="submit"]:not([form])');
  const updateButton = document.querySelector("#forecast-update-button");
  const empty = document.querySelector("#forecast-fixture-empty");
  const pendingSection = document.querySelector("#forecast-pending");
  const pendingList = document.querySelector("#forecast-pending-list");
  const pendingStatus = document.querySelector("#forecast-pending-status");
  const pendingValues = document.querySelector("#forecast-pending-values");
  const allFixtures = document.querySelector("#forecast-all-fixtures");
  if (!builder || !team || !fixture || outcomes.length === 0 || !addButton || !updateButton || !empty || !pendingSection || !pendingList || !pendingStatus || !pendingValues || !allFixtures) return;

  const options = Array.from(allFixtures.content.querySelectorAll("option"));
  const pending = new Map();

  const renderPending = () => {
    pendingList.replaceChildren();
    pendingValues.replaceChildren();
    pending.forEach(({ option, outcome: result }, fixtureID) => {
      const input = document.createElement("input");
      input.type = "hidden";
      input.name = "p";
      input.value = `${fixtureID}:${result}`;
      pendingValues.append(input);

      const item = document.createElement("li");
      const description = document.createElement("span");
      const resultLabel = document.createElement("strong");
      resultLabel.textContent = forecastOutcomeLabel(option, result);
      description.append(resultLabel, ` · ${option.textContent}`);
      const remove = document.createElement("button");
      remove.type = "button";
      remove.dataset.removeAssumption = fixtureID;
      remove.textContent = "Remove";
      item.append(description, remove);
      pendingList.append(item);
    });
    const hasPending = pending.size > 0;
    pendingSection.hidden = !hasPending;
    updateButton.disabled = !hasPending;
    updateButton.textContent = hasPending ? `Apply scenario (${pending.size})` : "Apply scenario";
    pendingStatus.textContent = hasPending ? `${pending.size} new ${pending.size === 1 ? "assumption" : "assumptions"} ready to apply.` : "";
  };

  const updateFixtures = () => {
    const selectedValue = fixture.value;
    const teamID = team.value;
    const visible = options.filter((option) => {
      return !pending.has(option.value) && (!teamID || option.dataset.homeTeamId === teamID || option.dataset.awayTeamId === teamID);
    });
    visible.forEach((option) => formatForecastFixtureLabel(option, teamID));
    fixture.replaceChildren(...visible);
    if (visible.some((option) => option.value === selectedValue)) fixture.value = selectedValue;
    else if (visible.length > 0) fixture.value = visible[0].value;

    const hasFixtures = visible.length > 0;
    fixture.disabled = !hasFixtures;
    outcomes.forEach((outcome) => { outcome.disabled = !hasFixtures; });
    addButton.disabled = !hasFixtures;
    empty.hidden = hasFixtures;
    updateForecastOutcomeLabels();
  };

  builder.addEventListener("submit", (event) => {
    event.preventDefault();
    const option = fixture.selectedOptions[0];
    const outcome = builder.querySelector('input[name="outcome"]:checked');
    if (!option || !outcome || !["h", "d", "a"].includes(outcome.value)) return;
    pending.set(option.value, { option, outcome: outcome.value });
    renderPending();
    updateFixtures();
  });
  pendingList.addEventListener("click", (event) => {
    const button = event.target.closest("button[data-remove-assumption]");
    if (!button) return;
    pending.delete(button.dataset.removeAssumption);
    renderPending();
    updateFixtures();
  });
  team.addEventListener("change", updateFixtures);
  fixture.addEventListener("change", updateForecastOutcomeLabels);
  renderPending();
  updateFixtures();
}

function setupForecastControls() {
  const controls = document.querySelector("[data-forecast-controls]");
  const form = controls?.querySelector("form[data-forecast-model-form]");
  const model = document.querySelector("#forecast-model");
  const comparison = document.querySelector("#forecast-comparison");
  const status = controls?.querySelector("[data-forecast-control-status]");
  if (!controls || !form || !model || !comparison) return;

  const syncComparison = () => {
    Array.from(comparison.options).forEach((option) => {
      option.disabled = Boolean(option.value) && option.value === model.value;
    });
    if (comparison.value === model.value) comparison.value = "";
  };

  const updateForecast = () => {
    controls.setAttribute("aria-busy", "true");
    if (status) status.textContent = "Updating forecast…";
    form.requestSubmit();
  };

  model.addEventListener("change", () => {
    syncComparison();
    updateForecast();
  });
  comparison.addEventListener("change", updateForecast);
  syncComparison();
}

function setupScenarioCopy() {
  const link = document.querySelector("[data-copy-scenario]");
  const label = link?.querySelector("[data-copy-scenario-label]");
  const status = document.querySelector("[data-scenario-copy-status]");
  if (!link || !label || !status || !navigator.clipboard?.writeText) return;

  link.addEventListener("click", async (event) => {
    event.preventDefault();
    try {
      await navigator.clipboard.writeText(new URL(link.href, window.location.href).href);
      status.textContent = "Scenario link copied to the clipboard.";
      const labelText = label.textContent;
      label.textContent = "Copied";
      window.setTimeout(() => {
        label.textContent = labelText;
      }, 1800);
    } catch {
      status.textContent = "Could not copy the scenario link. Open the link to copy it manually.";
      window.location.assign(link.href);
    }
  });
}

function setupClinchingTeamFilter() {
  const filter = document.querySelector("[data-clinching-team-filter]");
  const cards = Array.from(document.querySelectorAll("[data-clinching-team-card]"));
  const sections = Array.from(document.querySelectorAll("[data-clinching-filter-section]"));
  const summary = document.querySelector("[data-clinching-filter-summary]");
  if (!filter || cards.length === 0) return;

  const update = () => {
    const teamID = filter.value;
    const visibleCards = cards.filter((card) => !teamID || card.dataset.clinchingTeam === teamID);
    cards.forEach((card) => { card.hidden = !visibleCards.includes(card); });
    sections.forEach((section) => {
      section.hidden = Boolean(teamID) && !visibleCards.some((card) => section.contains(card));
    });
    if (!summary) return;
    if (!teamID) {
      summary.textContent = "All teams shown.";
      return;
    }
    const teamName = filter.selectedOptions[0]?.textContent ?? "this team";
    const label = visibleCards.length === 1 ? "scenario" : "scenarios";
    summary.textContent = `Showing ${visibleCards.length} ${label} for ${teamName}.`;
  };

  filter.addEventListener("change", update);
  update();
}

function setupFixtureTeamFilter() {
  const filter = document.querySelector("[data-fixture-team-filter]");
  const fixtures = Array.from(document.querySelectorAll("[data-fixture-home-team]"));
  const sections = Array.from(document.querySelectorAll("[data-fixture-filter-section]"));
  const summary = document.querySelector("[data-fixture-filter-summary]");
  if (!filter || fixtures.length === 0) return;

  const update = () => {
    const teamID = filter.value;
    const visibleFixtures = fixtures.filter((fixture) => !teamID || fixture.dataset.fixtureHomeTeam === teamID || fixture.dataset.fixtureAwayTeam === teamID);
    fixtures.forEach((fixture) => { fixture.hidden = !visibleFixtures.includes(fixture); });
    sections.forEach((section) => {
      section.hidden = Boolean(teamID) && !visibleFixtures.some((fixture) => section.contains(fixture));
    });
    if (!summary) return;
    if (!teamID) {
      summary.textContent = "All teams shown.";
      return;
    }
    const teamName = filter.selectedOptions[0]?.textContent ?? "this team";
    const label = visibleFixtures.length === 1 ? "fixture" : "fixtures";
    summary.textContent = `Showing ${visibleFixtures.length} ${label} for ${teamName}.`;
  };

  filter.addEventListener("change", update);
  update();
}

function setupFixtureViews() {
  const toggle = document.querySelector("[data-fixture-view-toggle]");
  const buttons = Array.from(document.querySelectorAll("[data-fixture-view-button]"));
  const views = Array.from(document.querySelectorAll("[data-fixture-view]"));
  if (!toggle || buttons.length === 0 || views.length === 0) return;

  const results = views.find((view) => view.dataset.fixtureView === "results");
  const upcoming = views.find((view) => view.dataset.fixtureView === "upcoming");
  if (!results || !upcoming) return;

  const now = new Date();
  const isToday = (date) => date.getFullYear() === now.getFullYear() &&
    date.getMonth() === now.getMonth() && date.getDate() === now.getDate();
  const groups = Array.from(upcoming.querySelectorAll("[data-fixture-group-start]"));
  const activeGroups = groups.filter((group) => {
    const kickoffs = Array.from(group.querySelectorAll("[data-local-time]")).map((fixture) => new Date(fixture.dataset.localTime));
    return kickoffs.some((kickoff) => !Number.isNaN(kickoff.getTime()) && (kickoff <= now || isToday(kickoff)));
  });
  activeGroups.forEach((group) => {
    const status = group.querySelector("[data-fixture-group-status]");
    if (status) {
      const hasStarted = Array.from(group.querySelectorAll("[data-local-time]")).some((fixture) => {
        const kickoff = new Date(fixture.dataset.localTime);
        return !Number.isNaN(kickoff.getTime()) && kickoff <= now;
      });
      status.textContent = hasStarted ? "In progress" : "Today";
      status.hidden = false;
    }
    results.prepend(group);
  });

  const show = (selected) => {
    views.forEach((view) => { view.hidden = view.dataset.fixtureView !== selected; });
    buttons.forEach((button) => { button.setAttribute("aria-pressed", String(button.dataset.fixtureViewButton === selected)); });
  };

  buttons.forEach((button) => {
    button.addEventListener("click", () => show(button.dataset.fixtureViewButton));
  });
  toggle.hidden = false;
  show("results");
}

localizeTimes();
setupForecastControls();
setupForecastAssumptionBuilder();
setupScenarioCopy();
setupClinchingTeamFilter();
setupFixtureTeamFilter();
setupFixtureViews();

function sortStandings(display, mode, stat) {
  const tbody = display.querySelector("[data-standings-table] tbody");
  if (!tbody) return;

  const rows = Array.from(tbody.rows);
  let playoffCutoff;
  if (stat === "xg") {
    const dataKey = mode === "per-game" ? "xgPerGame" : "xgTotal";
    const officialDataKey = mode === "per-game" ? "perGame" : "total";
    const officialCutoffRow = rows.find((row) => row.dataset[`${officialDataKey}PlayoffLine`] === "true");
    playoffCutoff = Number(officialCutoffRow?.querySelector("[data-standings-position]")?.dataset[officialDataKey]);
    rows.sort((left, right) => {
      const leftPoints = Number(left.querySelector("[data-standings-points]").dataset[dataKey]);
      const rightPoints = Number(right.querySelector("[data-standings-points]").dataset[dataKey]);
      const leftValue = Number.isNaN(leftPoints) ? -Infinity : leftPoints;
      const rightValue = Number.isNaN(rightPoints) ? -Infinity : rightPoints;
      if (leftValue !== rightValue) return rightValue - leftValue;
      const byName = left.dataset.teamName.localeCompare(right.dataset.teamName);
      if (byName !== 0) return byName;
      return left.dataset.teamId.localeCompare(right.dataset.teamId);
    });
  } else {
    const dataKey = mode === "per-game" ? "perGame" : "total";
    rows.sort((left, right) => Number(left.querySelector("[data-standings-position]").dataset[dataKey]) - Number(right.querySelector("[data-standings-position]").dataset[dataKey]));
  }
  rows.forEach((row) => tbody.append(row));
  rows.forEach((row, index) => {
    const position = row.querySelector("[data-standings-position]");
    if (stat === "xg") {
      position.textContent = index + 1;
      row.classList.toggle("playoff-line", index + 1 === playoffCutoff);
    } else {
      const dataKey = mode === "per-game" ? "perGame" : "total";
      position.textContent = position.dataset[dataKey];
      row.classList.toggle("playoff-line", row.dataset[`${dataKey}PlayoffLine`] === "true");
    }
  });
}
