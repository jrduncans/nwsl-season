document.addEventListener("change", (event) => {
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
    sortStandings(display, mode);
    display.querySelectorAll("[data-standings-mode-button]").forEach((candidate) => {
      candidate.setAttribute("aria-pressed", String(candidate === button));
    });
    display.querySelectorAll("[data-standings-value]").forEach((value) => {
      value.textContent = value.parentElement.dataset[mode === "per-game" ? "perGame" : "total"];
    });
    requestAnimationFrame(() => display.classList.remove("is-switching"));
  });
});

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
  const filter = document.querySelector("form[data-fixture-filter]");
  const builder = document.querySelector("form[data-assumption-builder]");
  const team = document.querySelector("#forecast-team");
  const fixture = document.querySelector("#forecast-fixture");
  const search = document.querySelector("#forecast-fixture-search");
  const fixtureCount = document.querySelector("#forecast-fixture-count");
  const outcomes = Array.from(builder?.querySelectorAll('input[name="outcome"]') ?? []);
  const addButton = builder?.querySelector('button[type="submit"]:not([form])');
  const updateButton = document.querySelector("#forecast-update-button");
  const empty = document.querySelector("#forecast-filter-empty");
  const pendingSection = document.querySelector("#forecast-pending");
  const pendingList = document.querySelector("#forecast-pending-list");
  const pendingStatus = document.querySelector("#forecast-pending-status");
  const pendingValues = document.querySelector("#forecast-pending-values");
  const allFixtures = document.querySelector("#forecast-all-fixtures");
  if (!filter || !builder || !team || !fixture || !search || !fixtureCount || outcomes.length === 0 || !addButton || !updateButton || !empty || !pendingSection || !pendingList || !pendingStatus || !pendingValues || !allFixtures) return;

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
    updateButton.textContent = hasPending ? `Update forecast (${pending.size})` : "Update forecast";
    pendingStatus.textContent = hasPending ? `${pending.size} new ${pending.size === 1 ? "assumption is" : "assumptions are"} ready. Update the forecast to apply ${pending.size === 1 ? "it" : "them"}.` : "";
  };

  const updateFixtures = () => {
    const selectedValue = fixture.value;
    const teamID = team.value;
    const query = search.value.trim().toLocaleLowerCase();
    const visible = options.filter((option) => {
      const fixtureText = `${option.dataset.fixtureLabel} ${option.dataset.homeTeam} ${option.dataset.awayTeam} ${option.dataset.kickoffFallback}`.toLocaleLowerCase();
      return !pending.has(option.value) && (!teamID || option.dataset.homeTeamId === teamID || option.dataset.awayTeamId === teamID) && (!query || fixtureText.includes(query));
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
    fixtureCount.textContent = `${visible.length} ${visible.length === 1 ? "fixture" : "fixtures"} available`;
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
  filter.addEventListener("submit", (event) => {
    event.preventDefault();
    updateFixtures();
  });
  team.addEventListener("change", updateFixtures);
  search.addEventListener("input", updateFixtures);
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
  const status = document.querySelector("[data-scenario-copy-status]");
  if (!link || !status || !navigator.clipboard?.writeText) return;

  link.addEventListener("click", async (event) => {
    event.preventDefault();
    try {
      await navigator.clipboard.writeText(new URL(link.href, window.location.href).href);
      status.textContent = "Scenario link copied to the clipboard.";
      const label = link.textContent;
      link.textContent = "Copied scenario link";
      window.setTimeout(() => {
        link.textContent = label;
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

localizeTimes();
setupForecastControls();
setupForecastAssumptionBuilder();
setupScenarioCopy();
setupClinchingTeamFilter();

function sortStandings(display, mode) {
  const tbody = display.querySelector("[data-standings-table] tbody");
  if (!tbody) return;

  const dataKey = mode === "per-game" ? "perGame" : "total";
  const rows = Array.from(tbody.rows);
  rows.sort((left, right) => Number(left.querySelector("[data-standings-position]").dataset[dataKey]) - Number(right.querySelector("[data-standings-position]").dataset[dataKey]));
  rows.forEach((row) => tbody.append(row));
  rows.forEach((row) => {
    const position = row.querySelector("[data-standings-position]");
    position.textContent = position.dataset[dataKey];
    row.classList.toggle("playoff-line", row.dataset[`${dataKey}PlayoffLine`] === "true");
  });
}
