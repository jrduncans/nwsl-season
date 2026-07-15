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
  return new Intl.DateTimeFormat(undefined, {
    weekday: "short",
    month: "short",
    day: "numeric",
    hour: "numeric",
    minute: "2-digit",
    timeZoneName: "short",
  }).format(date);
}

function localizeForecastTimes() {
  document.querySelectorAll("[data-local-time]").forEach((element) => {
    if (element.tagName === "OPTION") return;
    const localTime = formatLocalTime(element.dataset.localTime);
    if (!localTime) return;
    element.textContent = localTime;
  });
}

function updateForecastOutcomeLabels() {
  const fixture = document.querySelector("#forecast-fixture");
  const outcome = document.querySelector("#forecast-outcome");
  const selected = fixture?.selectedOptions[0];
  if (!selected || !outcome) return;
  outcome.querySelector('option[value="h"]').textContent = `${selected.dataset.homeTeam} win`;
  outcome.querySelector('option[value="a"]').textContent = `${selected.dataset.awayTeam} win`;
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
  const outcome = document.querySelector("#forecast-outcome");
  const addButton = builder?.querySelector('button[type="submit"]:not([form])');
  const updateButton = document.querySelector("#forecast-update-button");
  const empty = document.querySelector("#forecast-filter-empty");
  const pendingSection = document.querySelector("#forecast-pending");
  const pendingList = document.querySelector("#forecast-pending-list");
  const pendingValues = document.querySelector("#forecast-pending-values");
  if (!filter || !builder || !team || !fixture || !outcome || !addButton || !updateButton || !empty || !pendingSection || !pendingList || !pendingValues) return;

  const options = Array.from(fixture.options);
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
  };

  const updateFixtures = () => {
    const selectedValue = fixture.value;
    const teamID = team.value;
    const visible = options.filter((option) => !pending.has(option.value) && (!teamID || option.dataset.homeTeamId === teamID || option.dataset.awayTeamId === teamID));
    visible.forEach((option) => formatForecastFixtureLabel(option, teamID));
    fixture.replaceChildren(...visible);
    if (visible.some((option) => option.value === selectedValue)) fixture.value = selectedValue;
    else if (visible.length > 0) fixture.value = visible[0].value;

    const hasFixtures = visible.length > 0;
    fixture.disabled = !hasFixtures;
    outcome.disabled = !hasFixtures;
    addButton.disabled = !hasFixtures;
    empty.hidden = hasFixtures;
    updateForecastOutcomeLabels();
  };

  builder.addEventListener("submit", (event) => {
    event.preventDefault();
    const option = fixture.selectedOptions[0];
    if (!option || !["h", "d", "a"].includes(outcome.value)) return;
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

localizeForecastTimes();
setupForecastAssumptionBuilder();

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
