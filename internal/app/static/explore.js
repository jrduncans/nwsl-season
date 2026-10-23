(() => {
  const root = document.querySelector('[data-explore]');
  if (!root) return;
  // Switching the penalties basis swaps in the other basis's payloads and
  // redraws in place, or takes a full server render when the page has none.
  // The capture phase keeps the view forms' in-page change handlers from also
  // reacting.
  document.addEventListener('change', event => {
    const checkbox = event.target.closest?.('[data-penalties-selector]');
    if (!checkbox) return;
    event.stopPropagation();
    const url = new URL(location.href);
    if (checkbox.checked) url.searchParams.set('penalties', 'exclude'); else url.searchParams.delete('penalties');
    if (!switchesBasis(url.searchParams) || !useBasis(checkbox.checked)) { location.assign(url); return; }
    history.pushState(null, '', url);
    applyURL();
  }, true);
  // The server renders "<tab> · Explore · <site>"; tab names never contain " · ".
  const titleSuffix = document.title.slice(document.title.indexOf(' · ') + 3);
  let records = JSON.parse(root.querySelector('#explore-data').textContent) || [];
  let teamSeasons = JSON.parse(root.querySelector('#explore-team-data').textContent) || [];
  const h2hData = JSON.parse(root.querySelector('#explore-h2h-data').textContent) || [];
  let historyContextData = JSON.parse(root.querySelector('#explore-context-data').textContent);
  let homeRecords = JSON.parse(root.querySelector('#explore-home-data').textContent) || [];
  // The same four payloads on the basis the server did not render, or null.
  const otherBasis = JSON.parse(root.querySelector('#explore-other-basis').textContent);
  const thresholdRecords = JSON.parse(root.querySelector('#explore-threshold-data').textContent) || [];
  const parityRecords = JSON.parse(root.querySelector('#explore-parity-data').textContent) || [];
  // The view registry (explore_views.go): views in tab order, by group, with
  // the selection rules the server applies.
  const viewGroups = JSON.parse(root.querySelector('#explore-views').textContent);
  const views = new Map(viewGroups.flatMap(group => group.views.map(view => [view.key, {...view, group: group.key}])));
  const teamDisplays = views.get('teams').displays.map(display => display.key);
  // A rule matches when any alternative does: every named parameter, or the
  // view's default for a blank one, is an allowed value.
  const ruleMatches = (rule, params, defaults = {}) => (rule || []).some(alternative =>
    Object.entries(alternative).every(([name, allowed]) => allowed.includes(params.get(name) || defaults[name] || '')));
  // Scoring trend was once the default view, so its metric links omitted it.
  const requestedView = params => params.get('view') || (params.has('metric') ? 'trend' : viewGroups[0].views[0].key);
  // The Season by season chart card's controls submit with the form above it;
  // its segmented controls are RadioNodeLists.
  const historyPanel = root.querySelector('[data-panel="team-history"]');
  const historyControls = root.querySelector('[data-team-history-controls]');
  const historySeries = historyControls.elements.namedItem('series');
  const historyContext = root.querySelector('[data-history-context]');
  // Compare teams' chart card controls submit with the Season form; its
  // segmented controls are RadioNodeLists.
  const teamsPanel = root.querySelector('[data-panel="teams"]');
  const teamControls = root.querySelector('[data-team-controls]');
  const teamSeason = root.querySelector('[data-team-season]');
  const teamMeasure = teamControls.elements.namedItem('measure');
  const teamUnits = teamControls.elements.namedItem('units');
  const quadrantData = teamControls.elements.namedItem('quadrant-data');
  const quadrantLogoToggle = root.querySelector('[data-team-quadrant-logos]');
  const historyTeam = root.querySelector('[data-history-team]');
  const historyMeasure = historyControls.elements.namedItem('measure');
  const rankingControls = root.querySelector('[data-team-rankings-controls]');
  const trendControls = root.querySelector('[data-season-trend-controls]');
  const trendPanel = root.querySelector('[data-panel="season-trend"]');
  // Scoring trend's Show radios join no form (only this script draws the
  // chart), so metric reads and sets the checked one. It is absent without
  // trend data.
  const metricControl = root.querySelector('[data-panel="trend"] [data-segmented="metric"]');
  const metric = {
    get value() { return metricControl?.querySelector('input:checked')?.value || 'compare'; },
    set value(value) { const input = metricControl?.querySelector(`input[value="${value}"]`); if (input) input.checked = true; },
  };
  // A RadioNodeList, absent without distribution data.
  const distributionControls = root.querySelector('[data-distribution-controls]');
  const distributionBin = distributionControls?.elements.namedItem('distribution-bin');
  const status = root.querySelector('[data-chart-status]');
  const scatterLogoToggle = root.querySelector('[data-team-scatter-logos]');
  const scatterSelection = root.querySelector('[data-team-scatter-selection]');
  const scatterSelectionTitle = root.querySelector('[data-team-scatter-selection-title]');
  const scatterSelectionTeams = root.querySelector('[data-team-scatter-selection-teams]');
  const scatterGuideValues = root.querySelector('[data-team-scatter-guide-values]');
  const marginsData = window.NWSLMargins;
  const matchMeasure = marginsData.measure;
  const charts = {};
  const styles = getComputedStyle(root);
  const color = name => styles.getPropertyValue(name).trim();
  const goalsColor = color('--chart-actual'), xgColor = color('--chart-expected');
  // The server renders the page on the chosen penalties basis and embeds the
  // other basis's payloads; useBasis swaps them in. The script names the basis
  // with the same label rules as basisText in Go. BothBases views read both
  // bases from their own payloads, so the named basis (nonPenalty) follows the
  // URL while dataNonPenalty stays the basis of the payloads in use.
  const renderedNonPenalty = root.dataset.penalties === 'exclude';
  let nonPenalty = renderedNonPenalty, dataNonPenalty = renderedNonPenalty;
  // Team records name their matches by index into the shared match list,
  // which stores each match once in home orientation. Expand them into the
  // records exploreMatches builds in Go, on the payload's basis.
  const teamMatch = (fixture, teamID, np) => {
    const home = h2hData.teams[fixture.home].ID === teamID, side = home ? 0 : 1;
    const orient = pair => home ? pair : [pair[1], pair[0]];
    const opponentID = h2hData.teams[home ? fixture.away : fixture.home].ID;
    const score = orient(fixture.score), xg = orient(np ? fixture.npxg : fixture.xg);
    const goalsCovered = !np || fixture.npGoals !== null;
    const goals = np && goalsCovered ? orient(fixture.npGoals) : score;
    const margin = score[0] - score[1], factors = fixture.opponents?.[side];
    return {
      goalsCovered, undated: fixture.undated, venueDateUnavailable: fixture.venueDateUnavailable,
      id: fixture.id, date: fixture.date, opponent: h2hData.seasons[fixture.season].names[opponentID] || opponentID, opponentId: opponentID,
      venue: home ? 'Home' : 'Away', score: score.join('–'),
      values: [{actual: goals[0], expected: xg[0]}, {actual: goals[1], expected: xg[1]},
        {actual: goals[0] - goals[1], expected: xg[0] === null || xg[1] === null ? null : xg[0] - xg[1]},
        {actual: margin > 0 ? 3 : margin < 0 ? 0 : 1, expected: fixture.xpoints[side]}],
      opponents: factors && {scored: factors[0], allowed: factors[1], expectedPoints: factors[2]},
    };
  };
  const expandTeamMatches = (seasons, np) => seasons.forEach(season => (season.teams || []).forEach(team => {
    team.matches = (team.matches || []).map(index => teamMatch(h2hData.fixtures[index], team.id, np));
  }));
  expandTeamMatches(teamSeasons, renderedNonPenalty);
  const basisPayloads = {[renderedNonPenalty]: {records, teamSeasons, historyContextData, homeRecords,
    defaults: {defaultTeamSeason: root.dataset.defaultTeamSeason, defaultHistoryTeam: root.dataset.defaultHistoryTeam}}};
  if (otherBasis) {
    const payloads = {records: otherBasis.chartData || [], teamSeasons: otherBasis.teamSeasons || [], historyContextData: otherBasis.historyContext, homeRecords: otherBasis.homeAdvantage || [],
      defaults: {defaultTeamSeason: otherBasis.defaultTeamSeason, defaultHistoryTeam: otherBasis.defaultHistoryTeam}};
    expandTeamMatches(payloads.teamSeasons, !renderedNonPenalty);
    basisPayloads[!renderedNonPenalty] = payloads;
  }
  // useBasis puts the payloads for one basis in use, dropping every chart so
  // the next render rebuilds it from them. It reports false when the page
  // lacks that basis, which then needs a server render.
  // The view registry's rule for redrawing a selection on either basis.
  // Every view redraws from the payloads on either basis, except where the
  // server renders a different structure for a basis without the data: league
  // charts and tables, and team choices for a season without teams. Those
  // take a server render.
  function switchesBasis(params) {
    const target = basisPayloads[params.get('penalties') === 'exclude'];
    if (!target) return false;
    const rendered = basisPayloads[renderedNonPenalty];
    const view = requestedView(params);
    const hasBars = rows => rows.some(row => row.bins.some(Boolean));
    if (['trend', 'distribution', 'table'].includes(view)) {
      return Boolean(target.records.length) === Boolean(rendered.records.length) && hasBars(target.records) === hasBars(rendered.records);
    }
    if (view === 'home-advantage') return Boolean(target.homeRecords.length) === Boolean(rendered.homeRecords.length);
    if (!['teams', 'team-rankings', 'team-history', 'season-trend'].includes(view)) return true;
    return [target, rendered].every(payloads => {
      const season = params.get('season') || payloads.defaults.defaultTeamSeason;
      return Boolean(payloads.teamSeasons.find(row => row.season === season)?.teams?.length);
    });
  }
  // These charts read only payloads that carry both bases.
  const basisIndependentCharts = new Set(['thresholds', 'parity', 'archiveScatter', 'headToHead', 'headToHeadXG']);
  function useBasis(np) {
    if (np === dataNonPenalty) return true;
    if (!basisPayloads[np]) return false;
    ({records, teamSeasons, historyContextData, homeRecords} = basisPayloads[np]);
    Object.assign(root.dataset, basisPayloads[np].defaults);
    dataNonPenalty = np;
    indexHomeRecords();
    Object.keys(charts).filter(key => !basisIndependentCharts.has(key)).forEach(key => { charts[key].destroy(); delete charts[key]; });
    return true;
  }
  const basisPattern = /\b(?:\d\+? goals?|Expected goals|expected goals|xGD|xG|Goal differential|Goal margin|Total goals|Goals)\b/g;
  const basisReplacement = match => ({
    'Expected goals': 'Non-penalty expected goals', 'expected goals': 'non-penalty expected goals',
    xGD: 'npxGD', xG: 'npxG', 'Goal differential': 'Non-penalty goal differential', 'Goal margin': 'Non-penalty goal margin',
    'Total goals': 'Total non-penalty goals', Goals: 'Non-penalty goals',
  })[match] || match.replace(' ', ' non-penalty ');
  const T = text => nonPenalty && !/npxG|on-penalty/.test(text) ? text.replace(basisPattern, basisReplacement) : text;
  let goalsWord;
  const binColors = ['#44504a', '#526d61', color('--green'), color('--hypo'), '#372858'];
  // These labels name the current basis; applyURL relabels after a switch.
  let binLabels, teamMeasures, tableMeasures, scatterMeaning;
  function labelBasis() {
    goalsWord = nonPenalty ? 'non-penalty goals' : 'goals';
    binLabels = ['0 goals', '1 goal', '2 goals', '3 goals', '4+ goals'].map(T);
    teamMeasures = {
      for: {index: 0, actual: T('Goals'), expected: T('xG')},
      against: {index: 1, actual: T('Goals allowed'), expected: T('xG allowed')},
      difference: {index: 2, actual: T('Goal differential'), expected: T('xG differential')},
      points: {index: 3, actual: 'Points', expected: 'xPoints'},
    };
    tableMeasures = [teamMeasures.difference, teamMeasures.for, teamMeasures.against, teamMeasures.points];
    scatterMeaning = {
      for: T('Above: more goals scored than xG; below: fewer.'),
      against: T('Above: more goals conceded than xG allowed; below: fewer.'),
      difference: T('Above: higher goal differential than xG differential; below: lower.'),
      points: 'Above: more points earned than xPoints; below: fewer.',
    };
  }
  labelBasis();
  // Official points and ASA xPoints keep penalties; say so wherever they appear.
  const setPointsNote = (panel, params) => {
    const note = root.querySelector(`[data-points-note="${panel}"]`);
    const view = views.get(panel);
    if (note) note.hidden = !(nonPenalty && ruleMatches(view.pointsNote, params, view.defaults) && !isPointsOnlySelection(params));
  };
  function isPointsOnlySelection(params) {
    const view = views.get(requestedView(params));
    return Boolean(view) && ruleMatches(view.pointsOnly, params, view.defaults);
  }
  const fixed = value => (Math.abs(value) < .005 ? 0 : value).toFixed(2);
  const signed = value => `${value >= .005 ? '+' : ''}${fixed(value)}`;
  // Pure chart geometry lives in explore-geometry.js, which loads first.
  const {gapExtent, trendAxis, plotDomain, pointRadius, layoutLogos, stripRows} = window.NWSLGeometry;
  const percent = value => `${value.toFixed(1)}%`;
  // Use the same fit rule for visible labels and tooltip content.
  const hasSegmentLabel = (chart, value) => chart.chartArea && chart.chartArea.width * value / 100 >= 58;
  const commonOptions = () => ({
    responsive: true, maintainAspectRatio: false, animation: false,
    color: color('--muted'), font: {family: 'system-ui, sans-serif', size: 13},
    interaction: {mode: 'nearest', axis: 'xy', intersect: true},
    plugins: {
      legend: {display: false},
      tooltip: {
        backgroundColor: color('--ink'), padding: 12, cornerRadius: 5,
        titleFont: {size: 13}, bodyFont: {size: 13}, displayColors: false,
        // Coincident points still show one value, rather than a year-wide list.
        filter: (_, index) => index === 0,
      },
    },
  });
  // Disable Chart.js auto-padding because it changes plot bounds after redraws
  // in Safari. Keep the same small edge buffer reserved for points and labels.
  const scatterLayout = (top = 15, bottom = 0) => ({
    autoPadding: false,
    padding: {top, right: 15, bottom, left: 0},
  });
  function trendYAxis(gaps = null) {
    // The hidden bar dataset must not make the goals/xG line modes start at zero.
    const axis = {beginAtZero: false, grace: '10%', grid: {color: color('--line')}, border: {display: false}, ticks: {maxTicksLimit: 6}};
    if (gaps === null) return axis;
    const extent = gapExtent(gaps);
    return {...axis, grace: 0, min: -extent, max: extent,
      grid: {color: context => context.tick.value === 0 ? color('--muted') : color('--line')},
      // The symmetric limits are padding, not meaningful values to label.
      ticks: {maxTicksLimit: 7, includeBounds: false, callback: value => signed(value)},
      title: {display: true, text: T('Goals − xG per match')}};
  }
  const clearChart = chart => {
    chart.keyboardInspecting = false;
    chart.setActiveElements([]);
    chart.tooltip.setActiveElements([], {x: 0, y: 0});
    chart.update('none');
  };
  function clearScatterSelection(chart, update = true) {
    if (!chart) return;
    chart.scatterPinnedIndexes = [];
    chart.scatterGuideSource = null;
    chart.scatterProjectionHover = null;
    chart.canvas.style.cursor = '';
    chart.canvas.title = '';
    scatterSelection.hidden = true;
    scatterSelectionTeams.replaceChildren();
    scatterGuideValues.textContent = '';
    if (update) chart.update('none');
  }
  function dismiss(clearPinned = true) {
    if (clearPinned) clearArchiveIdentification(charts.archiveScatter, false);
    if (clearPinned) clearScatterSelection(charts.teamScatter, false);
    if (clearPinned) clearQuadrantSelection(charts.teamQuadrant, false);
    Object.values(charts).forEach(clearChart);
    status.textContent = '';
  }
  // Chart.js owns pointer/touch hit testing and tooltip placement. Its public
  // active-element API also lets keyboard users inspect the same marks.
  // A basis switch rebuilds charts on the same canvases, so each canvas
  // listens once and acts on its current chart.
  const keyboardCanvases = new WeakSet();
  function keyboardAccess(chart, describe, inspectOnFocus = false) {
    const canvas = chart.canvas;
    chart.keyboardDescribe = describe;
    if (keyboardCanvases.has(canvas)) return;
    keyboardCanvases.add(canvas);
    canvas.addEventListener('keydown', event => {
      const chart = Chart.getChart(canvas);
      if (!chart) return;
      if (event.key === 'Escape') { dismiss(); return; }
      if (!['ArrowLeft', 'ArrowRight', 'ArrowUp', 'ArrowDown', 'Home', 'End'].includes(event.key)) return;
      event.preventDefault();
      const marks = [];
      for (let index = 0; index < chart.data.labels.length; index++) {
        chart.data.datasets.forEach((dataset, datasetIndex) => {
          const value = dataset.data[index];
          if (chart.isDatasetVisible(datasetIndex) && (chart.keyboardIncludeMissing || value != null && !chart.getDatasetMeta(datasetIndex).data[index]?.skip) && (!dataset.keyboardOnce || index === 0) && (!chart.keyboardByIndex || marks.at(-1)?.index !== index)) marks.push({datasetIndex, index});
        });
      }
      if (!marks.length) return;
      const active = chart.getActiveElements()[0];
      let position = marks.findIndex(mark => active && mark.index === active.index && mark.datasetIndex === active.datasetIndex);
      if (event.key === 'Home') position = 0;
      else if (event.key === 'End') position = marks.length - 1;
      else position = Math.max(0, Math.min(marks.length - 1, position + (['ArrowLeft', 'ArrowUp'].includes(event.key) ? -1 : 1)));
      const mark = marks[position];
      const element = chart.getDatasetMeta(mark.datasetIndex).data[mark.index];
      chart.keyboardInspecting = true;
      chart.keyboardInspectionStartedAt = event.timeStamp;
      chart.setActiveElements([mark]);
      chart.tooltip.setActiveElements([mark], element.getCenterPoint());
      chart.update('none');
      status.textContent = chart.keyboardDescribe(mark);
    });
    if (inspectOnFocus) canvas.addEventListener('focus', () => {
      if (!Chart.getChart(canvas)?.getActiveElements().length) canvas.dispatchEvent(new KeyboardEvent('keydown', {key: 'Home'}));
    });
    canvas.addEventListener('blur', () => { const chart = Chart.getChart(canvas); if (chart) clearChart(chart); status.textContent = ''; });
  }
  function setTrendData(chart, rows) {
    const first = Number(rows[0].season), last = Number(rows.at(-1).season);
    const years = Array.from({length: last - first + 1}, (_, i) => String(first + i));
    const byYear = new Map(rows.map(row => [row.season, row]));
    chart.data.labels = years;
    chart.scoringRecords = byYear;
    chart.data.datasets.forEach(series => { series.data = years.map(year => byYear.get(year)?.[series.key] ?? null); });
  }
  function trendDescription(chart, mark) {
    if (chart.historyRows) { const inspection = historyInspection(chart, mark); return `${inspection.title}. ${inspection.lines.join('. ')}`; }
    const year = chart.data.labels[mark.index], series = chart.data.datasets[mark.datasetIndex];
    const row = chart.scoringRecords.get(year);
    const value = series.key === 'gap' ? signed(series.data[mark.index]) : fixed(series.data[mark.index]);
    return `${year}${row.active ? ' (in progress)' : ''}, ${series.label}: ${value} per match, ${row.played} played`;
  }
  function createTrend(canvas, rows = records, plugins = []) {
    const options = commonOptions();
    options.scales = {
      x: {grid: {display: false}, ticks: {maxRotation: 0, autoSkip: true, autoSkipPadding: 20, maxTicksLimit: 11}},
      y: trendYAxis(),
    };
    options.plugins.tooltip.callbacks = {
      title: items => {
        const item = items[0];
        return item ? `${item.label}${item.chart.scoringRecords.get(item.label)?.active ? ' (in progress)' : ''}` : '';
      },
      label: item => `${item.dataset.label}: ${item.dataset.key === 'gap' ? signed(item.parsed.y) : fixed(item.parsed.y)} per match`,
      afterLabel: item => `${item.chart.scoringRecords.get(item.label).played} played`,
    };
    const chart = new Chart(canvas, {
      type: 'line', options, plugins,
      data: {labels: [], datasets: [
        {label: T('Goals'), key: 'goals', borderColor: goalsColor, backgroundColor: goalsColor, pointStyle: 'circle'},
        {label: T('xG'), key: 'xg', borderColor: xgColor, backgroundColor: xgColor, pointStyle: 'rectRot'},
      ].map(series => ({
        ...series, data: [],
        spanGaps: false, borderWidth: 2.5, pointRadius: 5, pointHitRadius: 4,
        pointHoverRadius: 8, pointHoverBorderWidth: 2, pointHoverBorderColor: color('--panel'),
      }))},
    });
    setTrendData(chart, rows);
    chart.update('none');
    keyboardAccess(chart, mark => trendDescription(chart, mark));
    return chart;
  }
  function createLeagueTrend(canvas) {
    const chart = createTrend(canvas);
    chart.data.datasets.push({
      type: 'bar', label: T('Goals − xG'), key: 'gap', hidden: true,
      data: chart.data.labels.map(year => chart.scoringRecords.get(year)?.gap ?? null),
      backgroundColor: context => context.raw > 0 ? goalsColor : context.raw < 0 ? xgColor : color('--muted'),
      borderRadius: 2, maxBarThickness: 36, barPercentage: .7, categoryPercentage: .9, minBarLength: 3,
      hoverBorderColor: color('--gold'), hoverBorderWidth: 2,
    });
    chart.update('none');
    return chart;
  }
  function createDistribution(canvas) {
    canvas.parentElement.style.height = `${records.length * 54 + 45}px`;
    const options = commonOptions();
    options.indexAxis = 'y';
    options.plugins.tooltip.yAlign = 'bottom';
    options.scales = {
      x: {stacked: true, min: 0, max: 100, position: 'top', grid: {display: false}, border: {display: false}, ticks: {stepSize: 25, callback: value => `${value}%`}},
      y: {stacked: true, grid: {display: false}, border: {display: false}, ticks: {autoSkip: false, font: {weight: 'bold'}}},
    };
    options.plugins.datalabels = {
      color: '#fff', font: {size: 12, weight: 'bold'}, formatter: percent,
      display: context => hasSegmentLabel(context.chart, context.dataset.data[context.dataIndex]),
    };
    options.plugins.tooltip.callbacks = {
      title: () => '',
      label: item => {
        const row = records[item.dataIndex];
        const detail = `${item.dataset.label}: ${row.bins[item.datasetIndex]} of ${row.played} matches`;
        return hasSegmentLabel(item.chart, item.parsed.x) ? detail : [detail, percent(item.parsed.x)];
      },
    };
    const chart = new Chart(canvas, {
      type: 'bar', plugins: [ChartDataLabels], options,
      data: {labels: records.map(row => row.season), datasets: binLabels.map((label, index) => ({
        label, data: records.map(row => row.bins[index] / row.played * 100),
        backgroundColor: binColors[index], borderColor: color('--paper'), borderWidth: 1,
        hoverBorderColor: color('--gold'), hoverBorderWidth: 2, barThickness: 36,
      }))},
    });
    keyboardAccess(chart, mark => {
      const row = records[mark.index];
      return `${row.season}, ${binLabels[mark.datasetIndex]}: ${row.bins[mark.datasetIndex]} of ${row.played} matches, ${percent(row.bins[mark.datasetIndex] / row.played * 100)}`;
    });
    return chart;
  }
  function binTrendYAxis() {
    const largestShare = Math.max(...records.flatMap(row => row.bins.map(count => count / row.played * 100)));
    const paddedMax = Math.min(100, largestShare * 1.05);
    const step = paddedMax <= 40 ? 5 : paddedMax <= 60 ? 10 : 20;
    const max = Math.max(step, Math.min(100, Math.ceil(paddedMax / step) * step));
    return {min: 0, max, grid: {color: color('--line')}, border: {display: false},
      ticks: {stepSize: step, autoSkip: false, callback: value => `${value}%`},
      title: {display: true, text: 'Share of matches'}};
  }
  function createBinTrend(canvas) {
    const first = Number(records[0].season), last = Number(records.at(-1).season);
    const years = Array.from({length: last - first + 1}, (_, index) => String(first + index));
    const byYear = new Map(records.map(row => [row.season, row]));
    const options = commonOptions();
    options.scales = {
      x: {grid: {display: false}, ticks: {maxRotation: 0, autoSkip: true, autoSkipPadding: 20, maxTicksLimit: 11}},
      y: binTrendYAxis(),
    };
    options.plugins.tooltip.callbacks = {
      title: items => {
        const item = items[0];
        if (!item) return '';
        const row = item.chart.binRecords.get(item.label);
        return `${item.label}${row.active ? ' (in progress)' : ''}`;
      },
      label: item => {
        const chart = item.chart, row = chart.binRecords.get(item.label), bin = chart.binIndex;
        return `${binLabels[bin]}: ${row.bins[bin]} of ${row.played} matches (${percent(item.parsed.y)})`;
      },
    };
    const chart = new Chart(canvas, {type: 'line', options,
      data: {labels: years, datasets: [{
        label: '', data: years.map(() => null), spanGaps: false,
        borderColor: binColors[0], backgroundColor: binColors[0],
        pointBorderColor: binColors[0], pointBackgroundColor: binColors[0],
        borderWidth: 2.5, pointRadius: 5, pointHitRadius: 4,
        pointHoverRadius: 8, pointHoverBorderWidth: 2, pointHoverBorderColor: color('--panel'),
      }]},
    });
    chart.binRecords = byYear;
    keyboardAccess(chart, mark => {
      const year = chart.data.labels[mark.index], row = chart.binRecords.get(year), bin = chart.binIndex;
      return `${year}${row.active ? ' (in progress)' : ''}, ${binLabels[bin]}: ${row.bins[bin]} of ${row.played} matches, ${percent(row.bins[bin] / row.played * 100)}`;
    });
    return chart;
  }
  function showBinTrend(chart, bin) {
    const shares = chart.data.labels.map(year => {
      const row = chart.binRecords.get(year);
      return row ? row.bins[bin] / row.played * 100 : null;
    });
    chart.binIndex = bin;
    chart.data.datasets[0].label = binLabels[bin];
    chart.data.datasets[0].data = shares;
    chart.data.datasets[0].borderColor = binColors[bin];
    chart.data.datasets[0].backgroundColor = binColors[bin];
    chart.data.datasets[0].pointBorderColor = binColors[bin];
    chart.data.datasets[0].pointBackgroundColor = binColors[bin];
    chart.canvas.setAttribute('aria-label', `Share of regular-season matches with ${binLabels[bin]}, by season`);
    chart.resize(); chart.update();
  }
  // Home advantage: one panel per measure, sharing a season axis. Every mark is
  // home minus away per played match, so zero means the sides did equally well.
  let homeYears, homeByYear;
  function indexHomeRecords() {
    homeYears = homeRecords.length
      ? Array.from({length: Number(homeRecords.at(-1).season) - Number(homeRecords[0].season) + 1}, (_, index) => String(Number(homeRecords[0].season) + index))
      : [];
    homeByYear = new Map(homeRecords.map(row => [row.season, row]));
  }
  indexHomeRecords();
  const homeZeroLabel = {
    id: 'homeZeroLabel',
    afterDatasetsDraw(chart) {
      const {ctx, chartArea, scales} = chart;
      const y = scales.y.getPixelForValue(0);
      if (y < chartArea.top || y > chartArea.bottom) return;
      ctx.save();
      ctx.font = '12px system-ui, sans-serif';
      ctx.textAlign = 'left';
      ctx.textBaseline = 'bottom';
      ctx.lineJoin = 'round';
      ctx.lineWidth = 4;
      ctx.strokeStyle = color('--paper');
      ctx.fillStyle = color('--muted');
      const label = 'Zero: home and away equal';
      const x = chartArea.left + 6, top = y - 4 < chartArea.top + 14 ? y + 16 : y - 4;
      ctx.strokeText(label, x, top);
      ctx.fillText(label, x, top);
      ctx.restore();
    },
  };
  function homeYAxis(keys, title) {
    const values = homeRecords.flatMap(row => keys.map(key => row[key])).filter(value => value != null);
    const low = Math.min(0, ...values), high = Math.max(0, ...values);
    const pad = Math.max(.05, (high - low) * .15);
    return {
      min: low < 0 ? low - pad : 0, max: high > 0 ? high + pad : 0,
      afterFit: scale => { scale.width = 76; },
      grid: {color: context => context.tick.value === 0 ? color('--muted') : color('--line'), lineWidth: context => context.tick.value === 0 ? 2 : 1},
      border: {display: false},
      // The padded limits are headroom, not values to label.
      ticks: {maxTicksLimit: 6, includeBounds: false, callback: value => signed(value)},
      title: {display: true, text: title},
    };
  }
  function createHomePanel(canvas, series, title) {
    const options = commonOptions();
    options.scales = {
      x: {grid: {display: false}, ticks: {maxRotation: 0, autoSkip: true, autoSkipPadding: 20, maxTicksLimit: 11}},
      y: homeYAxis(series.map(item => item.key), title),
    };
    options.layout = {padding: {top: 8}};
    options.plugins.tooltip.callbacks = {
      title: items => {
        const item = items[0];
        return item ? `${item.label}${homeByYear.get(item.label)?.active ? ' (in progress)' : ''}` : '';
      },
      label: item => `${item.dataset.label}: ${signed(item.parsed.y)} per match`,
      afterLabel: item => {
        const row = homeByYear.get(item.label);
        return `${row.played} played · home W–D–L ${row.homeWins}–${row.draws}–${row.awayWins}`;
      },
    };
    const chart = new Chart(canvas, {
      type: 'line', options, plugins: [homeZeroLabel],
      data: {labels: homeYears, datasets: series.map(item => ({
        label: item.label, key: item.key, data: homeYears.map(year => homeByYear.get(year)?.[item.key] ?? null),
        borderColor: item.color, backgroundColor: item.color, pointStyle: item.pointStyle,
        spanGaps: false, borderWidth: 2.5, pointRadius: 5, pointHitRadius: 4,
        pointHoverRadius: 8, pointHoverBorderWidth: 2, pointHoverBorderColor: color('--panel'),
      }))},
    });
    keyboardAccess(chart, mark => {
      const year = chart.data.labels[mark.index], row = homeByYear.get(year);
      const dataset = chart.data.datasets[mark.datasetIndex];
      return `${year}${row.active ? ' (in progress)' : ''}, ${dataset.label}: ${signed(dataset.data[mark.index])} per match, home minus away, ${row.played} played`;
    });
    return chart;
  }
  function showHomeAdvantage() {
    const panels = [
      ['home-goals', 'homeGoals', [
        {key: 'goals', label: T('Goals'), color: goalsColor, pointStyle: 'circle'},
        {key: 'xg', label: T('xG'), color: xgColor, pointStyle: 'rectRot'},
      ], T('Goals, home − away per match')],
      ['home-points', 'homePoints', [
        {key: 'points', label: 'Points', color: goalsColor, pointStyle: 'circle'},
        {key: 'xpoints', label: 'xPoints', color: xgColor, pointStyle: 'rectRot'},
      ], 'Points, home − away per match'],
    ];
    panels.forEach(([name, key, series, title]) => {
      const canvas = root.querySelector(`[data-chart="${name}"]`);
      if (!canvas) return;
      const chart = charts[key] || (charts[key] = createHomePanel(canvas, series, title));
      chart.resize(); chart.update('none');
    });
  }
  // Source-verified historical cutoffs. The same unrounded values drive the
  // chart, native details and exact table; missing years never bridge lines.
  const thresholdBySeason = new Map(thresholdRecords.map(row => [row.season, row]));
  const thresholdYears = thresholdRecords.length
    ? Array.from({length: Number(thresholdRecords.at(-1).season) - Number(thresholdRecords[0].season) + 1}, (_, i) => String(Number(thresholdRecords[0].season) + i)) : [];
  let thresholdSelected = root.dataset.defaultThresholdSeason;
  function thresholdDescription(year) {
    const row = thresholdBySeason.get(year);
    return `${year}. ${row.values.map(value => value.ppg == null ? `${value.label}: ${value.reason}`
      : `${value.label}: ${value.cutoff.name}, place ${value.cutoff.place}, ${value.cutoff.officialPoints} points / ${value.cutoff.officialPlayed} matches, ${value.ppg.toFixed(3)} PPG. Next: ${value.next.name}, place ${value.next.place}, ${value.next.officialPoints} points / ${value.next.officialPlayed} matches, ${value.nextPPG.toFixed(3)} PPG${value.tied ? '. Equal-points tie across the boundary' : ''}`).join('. ')}`;
  }
  function selectThresholdSeason(year) {
    if (thresholdBySeason.has(year)) update({view: 'thresholds', 'threshold-season': year});
  }
  function createThresholds(canvas) {
    const options = commonOptions();
    options.interaction = {mode: 'index', axis: 'x', intersect: false};
    options.scales = {
      x: {grid: {display: false}, ticks: {maxRotation: 0, autoSkip: true, autoSkipPadding: 20, maxTicksLimit: 11}},
      y: {min: 0, max: 3, grid: {color: color('--line')}, border: {display: false}, ticks: {stepSize: .5}, title: {display: true, text: 'Points per game'}},
    };
    // Every coincident series remains in the tooltip; the selected season's
    // native details always preserve every boundary and adjacent identity.
    options.plugins.tooltip.filter = item => item.parsed.y != null;
    options.plugins.tooltip.callbacks = {
      title: items => {
        const year = items[0]?.label || '';
        const row = thresholdBySeason.get(year);
        return row ? `${year} · ${row.playoffs} playoff places · ${row.bye ? `${row.bye} byes` : 'No byes'}` : year;
      },
      label: item => {
        const value = thresholdBySeason.get(item.label).values[item.datasetIndex];
        return `${item.dataset.hoverLabel}: ${value.cutoff.name} · ${value.ppg.toFixed(3)} PPG`;
      },
    };
    options.onClick = (_, elements, chart) => { if (elements.length) selectThresholdSeason(chart.data.labels[elements[0].index]); };
    options.onHover = (event, elements) => { event.native.target.style.cursor = elements.length ? 'pointer' : ''; };
    const styles = [
      {color: '#136f4a', pointStyle: 'circle', borderDash: []},
      {color: '#1f6fa8', pointStyle: 'rect', borderDash: [8, 4]},
      {color: '#5b3f96', pointStyle: 'rectRot', borderDash: [2, 4]},
      {color: '#a44b18', pointStyle: 'cross', borderDash: [8, 3, 2, 3]},
    ];
    const chart = new Chart(canvas, {type: 'line', options, data: {
      labels: thresholdYears,
      datasets: styles.map((style, index) => ({
        label: thresholdRecords[0].values[index].label,
        hoverLabel: ['Shield', 'Home game', 'Bye', 'Playoffs'][index],
        data: thresholdYears.map(year => thresholdBySeason.get(year)?.values[index].ppg ?? null),
        pointStyle: style.pointStyle, borderDash: style.borderDash, borderColor: style.color,
        backgroundColor: style.color, pointBackgroundColor: color('--panel'),
        spanGaps: false, borderWidth: 2.5, pointBorderWidth: 2,
        pointRadius: context => thresholdYears[context.dataIndex] === thresholdSelected ? 8 : 5,
        pointHitRadius: 6, pointHoverRadius: 8,
      })),
    }});
    chart.keyboardByIndex = true;
    keyboardAccess(chart, mark => thresholdDescription(chart.data.labels[mark.index]));
    canvas.addEventListener('keydown', event => {
      const mark = chart.getActiveElements()[0];
      if (event.key === 'Enter' && mark) { event.preventDefault(); selectThresholdSeason(chart.data.labels[mark.index]); }
    });
    return chart;
  }
  function showThresholds(params) {
    const requested = params.get('threshold-season');
    thresholdSelected = thresholdBySeason.has(requested) ? requested : root.dataset.defaultThresholdSeason;
    root.querySelectorAll('[data-threshold-detail]').forEach(panel => { panel.hidden = panel.dataset.thresholdDetail !== thresholdSelected; });
    root.querySelectorAll('[data-threshold-year]').forEach(link => {
      if (link.dataset.thresholdYear === thresholdSelected) link.setAttribute('aria-current', 'true'); else link.removeAttribute('aria-current');
      const selection = new URLSearchParams(params);
      selection.set('view', 'thresholds'); selection.set('threshold-season', link.dataset.thresholdYear);
      link.href = `?${selection}`;
    });
    const outcomeKeys = ['shield', 'home', 'bye', 'playoffs'];
    const outcome = outcomeKeys.includes(params.get('threshold-outcome')) ? params.get('threshold-outcome') : 'playoffs';
    const sortKeys = ['season', 'team', 'points', 'played', 'ppg'];
    const sort = sortKeys.includes(params.get('threshold-sort')) ? params.get('threshold-sort') : 'season';
    const order = params.get('threshold-order') === 'asc' ? 'asc' : 'desc';
    const controls = root.querySelector('[data-threshold-controls]');
    controls.elements['threshold-season'].value = thresholdSelected;
    controls.elements['threshold-outcome'].value = outcome;
    controls.elements['threshold-sort'].value = sort;
    controls.elements['threshold-order'].value = order;
    const body = root.querySelector('[data-threshold-table-rows]');
    const rows = [...body.children];
    rows.sort((a, b) => {
      const comparison = sort === 'team' ? a.dataset.team.localeCompare(b.dataset.team)
        : Number(a.dataset[sort]) - Number(b.dataset[sort]);
      return comparison ? comparison * (order === 'asc' ? 1 : -1) : Number(b.dataset.season) - Number(a.dataset.season);
    });
    rows.forEach(row => { row.hidden = row.dataset.outcome !== outcome; });
    body.replaceChildren(...rows);
    root.querySelector('[data-threshold-table-empty]').hidden = rows.some(row => !row.hidden);
    root.querySelector('[data-threshold-table-label]').textContent = {shield: 'Shield', home: 'home playoff game', bye: 'playoff bye', playoffs: 'playoff'}[outcome];
    root.querySelectorAll('[data-threshold-sort]').forEach(link => {
      const key = link.dataset.thresholdSort, selected = key === sort;
      link.parentElement.setAttribute('aria-sort', selected ? (order === 'asc' ? 'ascending' : 'descending') : 'none');
      const selection = new URLSearchParams(params);
      selection.set('view', 'thresholds'); selection.set('threshold-season', thresholdSelected);
      selection.set('threshold-outcome', outcome); selection.set('threshold-sort', key);
      selection.set('threshold-order', selected ? (order === 'asc' ? 'desc' : 'asc') : key === 'team' ? 'asc' : 'desc');
      link.href = `?${selection}`;
    });
    if (params.has('threshold-outcome') || params.has('threshold-sort')) root.querySelector('[data-threshold-table]').open = true;
    const canvas = root.querySelector('[data-chart="thresholds"]');
    if (!thresholdRecords.length) return;
    const chart = charts.thresholds || (charts.thresholds = createThresholds(canvas));
    chart.resize(); chart.update('none');
  }
  // Parity: the spread of team points per game in each season, drawn over the
  // spread that chance alone would give, and one selected season's teams on a
  // PPG strip. Every value, including the quartiles, comes from the server.
  const parityYears = parityRecords.length
    ? Array.from({length: Number(parityRecords.at(-1).season) - Number(parityRecords[0].season) + 1}, (_, index) => String(Number(parityRecords[0].season) + index))
    : [];
  const parityBySeason = new Map(parityRecords.map(row => [row.season, row]));
  const paritySeasonSelect = root.querySelector('[data-parity-season]');
  // A RadioNodeList in the chart card, absent without parity data.
  const paritySeriesControl = root.querySelector('[data-parity-controls]')?.elements.namedItem('parity-series');
  const paritySeason = params => parityBySeason.has(params.get('season')) ? params.get('season') : root.dataset.defaultParitySeason;
  const paritySeries = params => params.get('parity-series') === 'both' ? 'both' : 'actual';
  // The strip's season, which the chart's marks emphasize.
  let paritySelected = '';
  const ppg = value => value == null ? 'Unavailable' : value.toFixed(2);
  const inProgress = row => row.active ? ` (in progress through ${row.played} matches)` : '';
  // The same sentence as exploreParityRecord.Summary in Go.
  const paritySummary = row => `Top-to-bottom gap: ${ppg(row.gap)} PPG, from ${row.top.join(', ')} (${ppg(row.teams[0].ppg)}) down to ${row.bottom.join(', ')} (${ppg(row.teams.at(-1).ppg)}). Middle half of teams: ${ppg(row.q1)} to ${ppg(row.q3)} PPG (interquartile range ${ppg(row.iqr)}).`;
  function parityDescription(year) {
    const row = parityBySeason.get(year);
    if (!row) return `${year}: no season`;
    const expected = row.xspread == null ? 'xPPG spread unavailable (xPoints incomplete)' : `xPPG spread ${ppg(row.xspread)}`;
    return `${year}${inProgress(row)}: PPG spread ${ppg(row.spread)}, ${row.spread > row.chance ? 'above' : 'within'} the chance level of ${ppg(row.chance)}; ${expected}; ${row.teams.length} teams, ${row.matches.toFixed(1)} matches each`;
  }
  function selectParitySeason(year) {
    if (parityBySeason.has(year)) update({view: 'parity', season: year});
  }
  function createParity(canvas) {
    const options = commonOptions();
    options.interaction = {mode: 'index', axis: 'x', intersect: false};
    options.scales = {
      x: {grid: {display: false}, ticks: {maxRotation: 0, autoSkip: true, autoSkipPadding: 20, maxTicksLimit: 11}},
      y: {min: 0, grace: '10%', grid: {color: color('--line')}, border: {display: false}, ticks: {maxTicksLimit: 6, callback: value => value.toFixed(2)},
        title: {display: true, text: 'Spread of points per game'}},
    };
    options.layout = {padding: {top: 8}};
    options.plugins.tooltip.filter = item => item.dataset.key !== 'chance' || item.parsed.y != null;
    options.plugins.tooltip.callbacks = {
      title: items => items[0] ? `${items[0].label}${parityBySeason.get(items[0].label)?.active ? ' (in progress)' : ''}` : '',
      label: item => `${item.dataset.label}: ${ppg(item.parsed.y)}`,
      footer: items => {
        const row = items[0] && parityBySeason.get(items[0].label);
        return row ? `${row.teams.length} teams, ${row.matches.toFixed(1)} matches each` : '';
      },
    };
    options.onClick = (_, elements, chart) => { if (elements.length) selectParitySeason(chart.data.labels[elements[0].index]); };
    options.onHover = (event, elements) => { event.native.target.style.cursor = elements.length ? 'pointer' : ''; };
    const values = key => parityYears.map(year => parityBySeason.get(year)?.[key] ?? null);
    const selected = context => parityYears[context.dataIndex] === paritySelected;
    const active = context => parityBySeason.get(parityYears[context.dataIndex])?.active;
    const mark = (base, hollow) => ({
      borderColor: base, pointBorderColor: base,
      // An active season is hollow, so it reads as a pace rather than a result.
      pointBackgroundColor: context => active(context) ? hollow : base, backgroundColor: base,
      pointRadius: context => selected(context) ? 8 : 5,
      pointBorderWidth: context => selected(context) ? 3 : 2,
      segment: {borderDash: context => parityBySeason.get(parityYears[context.p1DataIndex])?.active ? [5, 4] : undefined},
      spanGaps: false, borderWidth: 2.5, pointHitRadius: 6, pointHoverRadius: 8,
    });
    const chart = new Chart(canvas, {
      type: 'line', options,
      data: {labels: parityYears, datasets: [
        {label: 'PPG spread', key: 'spread', data: values('spread'), pointStyle: 'circle', order: 1, ...mark(goalsColor, color('--panel'))},
        {label: 'xPPG spread', key: 'xspread', data: values('xspread'), pointStyle: 'rectRot', order: 2, ...mark(xgColor, color('--panel'))},
        // Drawn first, behind both series: the region from zero to the chance level.
        {label: 'Chance level', key: 'chance', data: values('chance'), order: 3, fill: 'origin', spanGaps: false,
          backgroundColor: color('--chart-chance'), borderColor: color('--muted'), borderWidth: 1, borderDash: [4, 4],
          pointRadius: 0, pointHoverRadius: 0, pointHitRadius: 0},
      ]},
    });
    // Arrow keys step through seasons, not series; Enter shows that season.
    chart.keyboardByIndex = true;
    keyboardAccess(chart, mark => parityDescription(chart.data.labels[mark.index]));
    canvas.addEventListener('keydown', event => {
      if (event.key !== 'Enter' && event.key !== ' ') return;
      const mark = chart.getActiveElements()[0];
      if (!mark) return;
      event.preventDefault();
      selectParitySeason(chart.data.labels[mark.index]);
    });
    return chart;
  }
  // Draws the selected season's teams on a fixed 0–3 PPG axis, so seasons
  // compare at the same scale. Teams that would overlap stack into rows.
  function drawParityStrip(row, both) {
    const host = root.querySelector('[data-parity-strip]');
    if (!host) return;
    const svgNS = 'http://www.w3.org/2000/svg';
    const width = Math.max(280, host.clientWidth), left = 16, right = 16, radius = 11;
    const x = value => left + Math.max(0, Math.min(3, value)) / 3 * (width - left - right);
    const spans = row.teams.map(team => {
      const ends = [x(team.ppg)].concat(both && team.xppg != null ? [x(team.xppg)] : []);
      return {left: Math.min(...ends) - radius, right: Math.max(...ends) + radius};
    });
    const rows = stripRows(spans, 3);
    const rowCount = Math.max(1, ...rows.map(index => index + 1));
    const rowHeight = radius * 2 + 4, bracketTop = 14, plotTop = 58, plotBottom = plotTop + rowCount * rowHeight;
    const height = plotBottom + 46;
    const svg = document.createElementNS(svgNS, 'svg');
    svg.setAttribute('viewBox', `0 0 ${width} ${height}`);
    svg.setAttribute('width', width); svg.setAttribute('height', height);
    svg.setAttribute('aria-hidden', 'true');
    const add = (parent, name, attributes, text) => {
      const element = document.createElementNS(svgNS, name);
      Object.entries(attributes).forEach(([key, value]) => element.setAttribute(key, value));
      if (text != null) element.textContent = text;
      parent.append(element);
      return element;
    };
    const label = (text, at, y, anchor = 'middle') => {
      // Keep each label inside the strip, even at an axis end.
      const clamped = anchor === 'middle' ? Math.max(left + text.length * 3.2, Math.min(width - right - text.length * 3.2, at)) : at;
      return add(svg, 'text', {x: clamped, y, 'text-anchor': anchor, class: 'explore-parity-label'}, text);
    };
    // Middle half: the interquartile band behind the marks.
    add(svg, 'rect', {x: x(row.q1), y: plotTop - 4, width: Math.max(1, x(row.q3) - x(row.q1)), height: plotBottom - plotTop + 8, class: 'explore-parity-iqr'});
    label(`Middle half ${ppg(row.q1)}–${ppg(row.q3)}`, (x(row.q1) + x(row.q3)) / 2, plotTop - 8);
    // Top-to-bottom gap: a bracket from the worst to the best PPG.
    const low = x(row.teams.at(-1).ppg), high = x(row.teams[0].ppg), bracket = bracketTop + 8;
    add(svg, 'path', {d: `M${low} ${bracket + 6}V${bracket}H${high}V${bracket + 6}`, class: 'explore-parity-bracket'});
    label(`Top-to-bottom gap ${ppg(row.gap)}`, (low + high) / 2, bracketTop + 4);
    // Axis.
    add(svg, 'line', {x1: left, x2: width - right, y1: plotBottom + 4, y2: plotBottom + 4, class: 'explore-parity-axis'});
    const step = width < 420 ? 1 : .5;
    for (let tick = 0; tick <= 3; tick += step) {
      add(svg, 'line', {x1: x(tick), x2: x(tick), y1: plotBottom + 4, y2: plotBottom + 8, class: 'explore-parity-axis'});
      label(tick.toFixed(1), x(tick), plotBottom + 22, tick === 0 ? 'start' : tick === 3 ? 'end' : 'middle');
    }
    label('Points per game', width / 2, plotBottom + 40);
    row.teams.forEach((team, index) => {
      const y = plotTop + rows[index] * rowHeight + radius;
      const group = add(svg, 'g', {class: 'explore-parity-team'});
      add(group, 'title', {}, `${team.name}: ${ppg(team.ppg)} PPG${both ? `, ${ppg(team.xppg)} xPPG` : ''}, ${team.played} played`);
      if (both && team.xppg != null) {
        add(group, 'line', {x1: x(team.ppg), x2: x(team.xppg), y1: y, y2: y, class: 'explore-parity-link'});
        const cx = x(team.xppg), size = radius - 1;
        add(group, 'path', {d: `M${cx} ${y - size}L${cx + size} ${y}L${cx} ${y + size}L${cx - size} ${y}Z`, class: 'explore-parity-expected'});
      }
      add(group, 'circle', {cx: x(team.ppg), cy: y, r: radius, class: `explore-parity-actual${row.active ? ' is-active' : ''}${team.logo ? ' has-logo' : ''}`});
      // A logo names the team; the ring under it keeps the mark if it fails to load.
      if (team.logo) {
        const logo = add(group, 'image', {href: team.logo, x: x(team.ppg) - radius + 3, y: y - radius + 3, width: radius * 2 - 6, height: radius * 2 - 6, class: 'explore-parity-logo'});
        logo.addEventListener('error', () => logo.remove());
      }
    });
    host.replaceChildren(svg);
    host.setAttribute('aria-label', `${row.season}${inProgress(row)}: ${row.teams.length} teams on a points-per-game scale from 0 to 3. ${paritySummary(row)}`);
  }
  function showParity(params) {
    const canvas = root.querySelector('[data-chart="parity"]');
    if (!canvas) return;
    const year = paritySeason(params), series = paritySeries(params), row = parityBySeason.get(year);
    paritySelected = year;
    const chart = charts.parity || (charts.parity = createParity(canvas));
    chart.setDatasetVisibility(1, series === 'both');
    chart.resize(); chart.update('none');
    if (document.activeElement === canvas) {
      const index = chart.data.labels.indexOf(year);
      chart.setActiveElements([{datasetIndex: 0, index}]);
      chart.update('none');
    }
    paritySeasonSelect.value = year;
    paritySeriesControl.value = series;
    root.querySelector('[data-parity-expected-legend]').hidden = series !== 'both';
    const requested = params.get('season');
    const note = root.querySelector('[data-parity-season-note]');
    note.hidden = !requested || requested === year;
    note.textContent = note.hidden ? '' : `${requested} is not plotted, so the strip shows ${year}.`;
    root.querySelector('[data-parity-strip-title]').textContent = `Teams in ${year}${inProgress(row)}`;
    root.querySelector('[data-parity-summary]').textContent = paritySummary(row);
    root.querySelector('[data-parity-team-caption]').textContent = `PPG and xPPG for each team in ${year}, highest PPG first.`;
    root.querySelector('[data-parity-teams]').replaceChildren(...row.teams.map(team => {
      const tr = document.createElement('tr');
      const name = document.createElement('th'); name.scope = 'row'; name.textContent = team.name;
      tr.append(name, ...[team.played, ppg(team.ppg), ppg(team.xppg)].map(value => {
        const cell = document.createElement('td'); cell.textContent = value; return cell;
      }));
      return tr;
    }));
    drawParityStrip(row, series === 'both');
  }
  let parityResize = 0;
  window.addEventListener('resize', () => {
    cancelAnimationFrame(parityResize);
    parityResize = requestAnimationFrame(() => {
      if (!root.querySelector('[data-panel="parity"]')?.hidden) showParity(new URL(location.href).searchParams);
    });
  });
  function teamDescription(chart, index) {
    const row = chart.teamRows[index], measure = chart.teamMeasure;
    const units = chart.teamUnits || 'per-match';
    const value = teamMetric(row, measure, units);
    const suffix = units === 'total' ? ' total' : ' per match';
    return [
      `${measure.actual}: ${units === 'total' ? value.actual : fixed(value.actual)}${suffix}`,
      `${measure.expected}: ${value.expected === null ? 'unavailable' : `${fixed(value.expected)}${suffix}`}`,
      `Gap: ${value.expected === null ? 'unavailable' : `${signed(value.actual - value.expected)}${suffix}`}`,
      `${row.played} played`,
    ];
  }
  function teamName(row) {
    const label = document.createElement('span');
    label.className = 'team-name';
    if (row.logo) {
      const logo = document.createElement('img');
      logo.className = 'team-logo'; logo.alt = ''; logo.loading = 'lazy'; logo.src = row.logo;
      label.append(logo);
    }
    const name = document.createElement('span');
    name.textContent = row.name; label.append(name);
    return label;
  }
  function teamMetric(row, measure, units = 'per-match') {
    return (units === 'total' ? row.totals : row.values)[measure.index];
  }
  function teamValue(row, measure, column, units = 'per-match') {
    const value = teamMetric(row, measure, units);
    if (column === 'name') return row.name;
    if (column === 'played') return row.played;
    if (column === 'gap') return value.expected === null ? null : value.actual - value.expected;
    return value[column];
  }
  function teamSortMetric(key) {
    for (const name of ['difference', 'for', 'against', 'points']) {
      if (key.startsWith(`${name}-`)) return {measure: teamMeasures[name], column: key.slice(name.length + 1)};
    }
    return {measure: teamMeasures.difference, column: key};
  }
  function sortedTeams(rows, measure, column, order, units = 'per-match') {
    return [...rows].sort((a, b) => {
      const x = teamValue(a, measure, column, units), y = teamValue(b, measure, column, units);
      if (x === null || y === null) {
        if (x !== y) return (x === null) - (y === null);
      } else if (x !== y) {
        return (x < y ? -1 : 1) * (order === 'asc' ? 1 : -1);
      }
      return a.name.localeCompare(b.name) || a.id.localeCompare(b.id);
    });
  }
  function teamLink(changes) {
    const params = new URLSearchParams(location.search);
    params.set('view', 'teams');
    Object.entries(changes).forEach(([key, value]) => params.set(key, value));
    return `?${params}`;
  }
  function teamYAxis(labels) {
    return {offset: true, grid: {color: color('--line'), drawTicks: false}, border: {display: false}, ticks: {autoSkip: false, display: false}, afterFit: scale => {
      scale.width = root.querySelector(labels).offsetWidth;
    }};
  }
  function alignTeamLabels(chart, selector) {
    root.querySelector(selector).childNodes.forEach((label, index) => {
      label.style.top = `${chart.scales.y.getPixelForValue(index)}px`;
    });
  }
  function teamGapXAxis(values, units = 'per-match') {
    const extent = gapExtent(values);
    return {min: -extent, max: extent, position: 'top',
      grid: {color: context => context.tick.value === 0 ? color('--muted') : color('--line')},
      border: {display: false},
      ticks: {includeBounds: false, maxTicksLimit: 7, callback: value => signed(value)},
      title: {display: true, text: units === 'total' ? 'Total actual − expected' : 'Actual − expected per match'}};
  }
  function createTeams(canvas) {
    const options = commonOptions();
    options.indexAxis = 'y';
    options.scales = {
      x: {beginAtZero: true, position: 'top', grace: '10%', grid: {color: color('--line')}, border: {display: false}, ticks: {maxTicksLimit: 5}, title: {display: true, text: 'Per match'}},
      y: teamYAxis('[data-team-labels]'),
    };
    options.plugins.tooltip.callbacks = {
      title: items => items[0] ? items[0].chart.teamRows[items[0].dataIndex].name : '',
      label: item => teamDescription(item.chart, item.dataIndex),
    };
    const chart = new Chart(canvas, {
      type: 'line', options,
      plugins: [{id: 'teamConnectors', afterLayout(chart) {
        alignTeamLabels(chart, '[data-team-labels]');
      }, beforeDatasetsDraw(chart) {
        const actual = chart.getDatasetMeta(0).data, expected = chart.getDatasetMeta(1).data;
        const ctx = chart.ctx;
        ctx.save(); ctx.strokeStyle = color('--muted'); ctx.lineWidth = 2;
        actual.forEach((point, index) => {
          if (point.skip || expected[index]?.skip) return;
          ctx.beginPath(); ctx.moveTo(point.x, point.y); ctx.lineTo(expected[index].x, expected[index].y); ctx.stroke();
        });
        ctx.restore();
      }}],
      data: {labels: [], datasets: [
        {label: 'Actual', backgroundColor: goalsColor, borderColor: goalsColor, pointStyle: 'circle', pointRadius: 4.5},
        {label: 'Expected', backgroundColor: xgColor, borderColor: xgColor, pointStyle: 'rectRot', pointRadius: 6},
      ].map(series => ({...series, data: [], showLine: false, pointHitRadius: 6, pointHoverRadius: 8}))},
    });
    keyboardAccess(chart, mark => `${chart.teamRows[mark.index].name}. ${teamDescription(chart, mark.index).join('. ')}`);
    return chart;
  }
  function createTeamGap(canvas) {
    const options = commonOptions();
    options.indexAxis = 'y';
    options.scales = {x: teamGapXAxis([]), y: teamYAxis('[data-team-gap-labels]')};
    options.plugins.tooltip.callbacks = {
      title: items => items[0] ? items[0].chart.teamRows[items[0].dataIndex].name : '',
      label: item => teamDescription(item.chart, item.dataIndex),
    };
    const chart = new Chart(canvas, {
      type: 'bar', options,
      plugins: [{id: 'teamGapLabels',
        afterLayout(chart) { alignTeamLabels(chart, '[data-team-gap-labels]'); },
        afterDatasetsDraw(chart) {
          if (!chart.teamRows) return;
          const ctx = chart.ctx, zero = chart.scales.x.getPixelForValue(0);
          ctx.save(); ctx.fillStyle = color('--muted'); ctx.font = '12px system-ui, sans-serif'; ctx.textBaseline = 'middle';
          chart.teamRows.forEach((row, index) => {
            const gap = teamValue(row, chart.teamMeasure, 'gap', chart.teamUnits);
            if (gap === null || Math.abs(gap) < .005) {
              ctx.fillText(gap === null ? (chart.teamMeasure.index === 3 ? 'xPoints incomplete' : T('xG incomplete')) : '0.00',
                gap === null ? chart.chartArea.left + 8 : zero + 8, chart.scales.y.getPixelForValue(index));
            }
          });
          ctx.restore();
        },
      }],
      data: {labels: [], datasets: [{
        label: 'Actual − expected', data: [], barThickness: 24, borderRadius: 2,
        backgroundColor: context => context.raw > 0 ? goalsColor : context.raw < 0 ? xgColor : color('--muted'),
        hoverBorderColor: color('--gold'), hoverBorderWidth: 2,
      }]},
    });
    keyboardAccess(chart, mark => `${chart.teamRows[mark.index].name}. ${teamDescription(chart, mark.index).join('. ')}`);
    return chart;
  }
  function scatterDomain(rows, measure, units, roundBounds = false) {
    const values = rows.flatMap(row => {
      const metric = teamMetric(row, measure, units);
      return [metric.actual, metric.expected];
    });
    return plotDomain(values, measure.index === 2, roundBounds);
  }
  function scatterMarks(chart) {
    return chart.data.datasets.flatMap((dataset, datasetIndex) => chart.isDatasetVisible(datasetIndex)
      ? dataset.data.flatMap((value, index) => value?.x != null && value?.y != null
        ? [{index, datasetIndex, x: chart.scales.x.getPixelForValue(value.x), y: chart.scales.y.getPixelForValue(value.y)}] : []) : []);
  }
  function fitScatterPoints(chart) {
    chart.scatterPointRadius = pointRadius(scatterMarks(chart), chart.chartArea);
  }
  function drawScatterLogos(chart) {
    const layer = chart.canvas.parentElement.querySelector('[data-team-scatter-logo-layer]');
    if (!layer) return;
    layer.hidden = !chart.scatterShowLogos;
    if (!chart.scatterShowLogos) return;
    const rows = chart.teamRows || [];
    const logoRows = rows.filter(row => row.logo);
    const images = chart.scatterLogoImages || (chart.scatterLogoImages = new Map());
    const logoKey = logoRows.map(row => `${row.id}\0${row.logo}`).join('\1');
    if (chart.scatterLogoKey !== logoKey) {
      const logoElements = logoRows.map(row => {
        let image = images.get(row.id);
        if (!image) {
          image = document.createElement('img');
          image.className = 'explore-scatter-logo';
          image.alt = '';
          image.draggable = false;
          images.set(row.id, image);
        }
        if (image.dataset.logo !== row.logo) {
          image.dataset.logo = row.logo;
          image.src = row.logo;
        }
        image.title = row.name;
        image.style.visibility = 'hidden';
        return image;
      });
      layer.replaceChildren(...logoElements);
      chart.scatterLogoKey = logoKey;
    }
    for (const image of layer.children) image.style.visibility = 'hidden';
    const anchorDataset = chart.data.datasets.findIndex((_, index) => chart.isDatasetVisible(index));
    if (anchorDataset < 0) return;
    const obstacles = scatterMarks(chart);
    const points = obstacles.filter(point => point.datasetIndex === anchorDataset);
    const radius = (chart.scatterPointRadius || 6) + 3;
    const canvasBounds = chart.canvas.getBoundingClientRect();
    const layerBounds = layer.getBoundingClientRect();
    const offsetX = canvasBounds.left - layerBounds.left;
    const offsetY = canvasBounds.top - layerBounds.top;
    // Establish small labels first, then enlarge without displacing neighbors.
    const usable = point => {
      const image = rows[point.index]?.logo && images.get(rows[point.index].id);
      return Boolean(image) && !(image.complete && !image.naturalWidth);
    };
    for (const label of layoutLogos(points, obstacles, chart.chartArea, radius, usable)) {
      const {left, top} = label.rect;
      const image = images.get(rows[label.point.index].id);
      image.style.width = image.style.height = `${label.rect.right - left}px`;
      image.style.left = `${left + offsetX}px`;
      image.style.top = `${top + offsetY}px`;
      image.style.visibility = 'visible';
    }
  }
  function showScatterSelection(chart, indexes) {
    const selected = [...new Set(indexes)].filter(index => chart.teamRows[index]);
    if (!selected.length) {
      clearScatterSelection(chart);
      return;
    }
    chart.scatterPinnedIndexes = selected;
    chart.scatterGuideSource = 'point';
    chart.scatterProjectionHover = null;
    chart.canvas.title = '';
    scatterSelectionTitle.textContent = selected.length === 1 ? 'Selected point' : `${selected.length} teams at this point`;
    scatterSelectionTeams.replaceChildren(...selected.map(index => {
      const row = chart.teamRows[index];
      const value = teamMetric(row, chart.teamMeasure, chart.teamUnits);
      const suffix = chart.teamUnits === 'total' ? ' total' : ' per match';
      const display = number => chart.teamUnits === 'total' ? String(number) : fixed(number);
      const card = document.createElement('article');
      card.className = 'explore-scatter-selection-team';
      const name = document.createElement('h4');
      name.textContent = row.name;
      card.append(name);
      [
        `${chart.teamMeasure.expected}: ${display(value.expected)}${suffix}`,
        `${chart.teamMeasure.actual}: ${display(value.actual)}${suffix}`,
        `Gap: ${signed(value.actual - value.expected)}${suffix}`,
        `${row.played} played`,
      ].forEach(line => {
        const detail = document.createElement('p');
        detail.textContent = line;
        card.append(detail);
      });
      return card;
    }));
    updateScatterGuideValues(chart);
    scatterSelection.hidden = false;
    chart.update('none');
  }
  function scatterProjectionPoints(chart) {
    const index = chart.scatterPinnedIndexes?.[0];
    const row = chart.teamRows?.[index];
    if (!row) return [];
    const values = teamMetric(row, chart.teamMeasure, chart.teamUnits);
    return [
      {source: 'expected', value: values.expected, label: chart.teamMeasure.expected},
      {source: 'actual', value: values.actual, label: chart.teamMeasure.actual},
    ];
  }
  function scatterProjectionAt(chart, x, y) {
    if (!Number.isFinite(x) || !Number.isFinite(y)) return null;
    const targets = scatterProjectionPoints(chart).map(target => ({
      ...target,
      x: chart.scales.x.getPixelForValue(target.value),
      y: chart.scales.y.getPixelForValue(target.value),
    }));
    return targets.map(target => ({...target, distance: Math.hypot(target.x - x, target.y - y)}))
      .filter(target => target.distance <= 11)
      .sort((a, b) => a.distance - b.distance)[0] || null;
  }
  function updateScatterGuideValues(chart) {
    const row = chart.teamRows?.[chart.scatterPinnedIndexes?.[0]];
    if (!row) {
      scatterGuideValues.textContent = '';
      return;
    }
    const value = teamMetric(row, chart.teamMeasure, chart.teamUnits);
    const expected = chart.scatterGuideSource === 'expected' ? value.expected :
      chart.scatterGuideSource === 'actual' ? value.actual : value.expected;
    const actual = chart.scatterGuideSource === 'expected' ? value.expected : value.actual;
    const suffix = chart.teamUnits === 'total' ? ' total' : ' per match';
    const display = number => chart.teamUnits === 'total' ? String(number) : fixed(number);
    scatterGuideValues.textContent = `Guides: ${chart.teamMeasure.expected} ${display(expected)} · ${chart.teamMeasure.actual} ${display(actual)}${suffix}`;
  }
  function createTeamScatter(canvas) {
    const options = commonOptions();
    options.layout = scatterLayout();
    options.interaction = {mode: 'point', intersect: true};
    options.onClick = (event, activeElements, chart) => {
      const target = scatterProjectionAt(chart, event.x, event.y);
      const nearbyPoints = activeElements.filter(mark => mark.datasetIndex === 0).map(mark => {
        const center = chart.getDatasetMeta(0).data[mark.index].getCenterPoint();
        return Math.hypot(center.x - event.x, center.y - event.y);
      });
      // A visible team dot remains the first click target when it overlaps a guide marker.
      if (target && (!nearbyPoints.length || Math.min(...nearbyPoints) > 8)) {
        chart.scatterGuideSource = target.source;
        updateScatterGuideValues(chart);
        chart.update('none');
        return;
      }
      const points = activeElements.filter(mark => mark.datasetIndex === 0).map(mark => {
        const center = chart.getDatasetMeta(0).data[mark.index].getCenterPoint();
        const x = Number.isFinite(event.x) ? event.x : center.x;
        const y = Number.isFinite(event.y) ? event.y : center.y;
        return {index: mark.index, distance: Math.hypot(center.x - x, center.y - y)};
      });
      if (!points.length) {
        showScatterSelection(chart, []);
        return;
      }
      // Point-mode hit testing can include nearby dots; pin the nearest dot and
      // retain every team only when their centers share the same location.
      const nearest = Math.min(...points.map(point => point.distance));
      showScatterSelection(chart, points
        .filter(point => point.distance <= nearest + .25)
        .map(point => point.index));
    };
    options.onHover = (event, activeElements, chart) => {
      const target = scatterProjectionAt(chart, event.x, event.y);
      const source = target?.source || null;
      const projectionChanged = chart.scatterProjectionHover !== source;
      chart.scatterProjectionHover = source;
      chart.canvas.style.cursor = target ? 'crosshair' : activeElements.length ? 'pointer' : '';
      chart.canvas.title = target ? `Click to set both guides to ${target.label} ${chart.teamUnits === 'total' ? String(target.value) : fixed(target.value)}` : '';
      if (projectionChanged && chart.scatterPinnedIndexes?.length) chart.update('none');
    };
    options.scales = {
      x: {type: 'linear', grid: {color: color('--line')}, border: {display: false},
        ticks: {includeBounds: false, maxTicksLimit: 6}, title: {display: true, text: 'Expected per match'}},
      y: {type: 'linear', grid: {color: color('--line')}, border: {display: false},
        ticks: {includeBounds: false, maxTicksLimit: 6}, title: {display: true, text: 'Actual per match'}},
    };
    // Point mode exposes teams that share a location instead of silently picking one.
    options.plugins.tooltip.filter = (_, index) => index < 3;
    options.plugins.tooltip.callbacks = {
      title: items => items.length === 1 ? '' : 'Nearby teams',
      label: item => [item.chart.teamRows[item.dataIndex].name, ...teamDescription(item.chart, item.dataIndex)],
      footer: items => {
        const overlapping = items[0]?.chart.getActiveElements().length || 0;
        return overlapping > items.length ? `+${overlapping - items.length} more; see the table below the plot` : '';
      },
    };
    options.plugins.datalabels = {
      align: 'top', anchor: 'center', clamp: true, clip: false,
      color: color('--ink'), backgroundColor: color('--panel'), borderColor: color('--line'),
      borderRadius: 3, borderWidth: 1, padding: 3,
      font: {size: 11, weight: '600'},
      display: context => {
        if (context.chart.scatterShowLogos) return false;
        if (context.chart.scatterPinnedIndexes?.[0] === context.dataIndex) return true;
        return context.active ? 'auto' : false;
      },
      formatter: (_, context) => context.chart.teamRows?.[context.dataIndex]?.name || '',
    };
    const chart = new Chart(canvas, {
      type: 'scatter', options,
      plugins: [{id: 'teamScatterParity',
        afterLayout: fitScatterPoints,
        beforeDatasetsDraw(chart) {
          const {x, y} = chart.scales, ctx = chart.ctx;
          ctx.save(); ctx.strokeStyle = color('--muted'); ctx.lineWidth = 1.5; ctx.setLineDash([6, 5]);
          ctx.beginPath(); ctx.moveTo(x.getPixelForValue(x.min), y.getPixelForValue(x.min));
          ctx.lineTo(x.getPixelForValue(x.max), y.getPixelForValue(x.max)); ctx.stroke(); ctx.restore();
        },
        afterDatasetsDraw(chart) {
          drawScatterLogos(chart);
          const selectedIndex = chart.scatterPinnedIndexes?.[0]
            ?? chart.getActiveElements().find(mark => mark.datasetIndex === 0)?.index;
          if (selectedIndex === undefined) return;
          const point = chart.getDatasetMeta(0).data[selectedIndex];
          if (!point || point.skip) return;
          const area = chart.chartArea;
          const source = chart.scatterPinnedIndexes?.length ? chart.scatterGuideSource : 'point';
          const row = chart.teamRows[selectedIndex];
          const value = teamMetric(row, chart.teamMeasure, chart.teamUnits);
          const guideX = source === 'actual' ? value.actual : value.expected;
          const guideY = source === 'expected' ? value.expected : value.actual;
          const x = chart.scales.x.getPixelForValue(guideX);
          const y = chart.scales.y.getPixelForValue(guideY);
          const ctx = chart.ctx;
          ctx.save(); ctx.strokeStyle = color('--gold'); ctx.lineWidth = 1.5; ctx.setLineDash([4, 4]);
          ctx.beginPath(); ctx.moveTo(area.left, y); ctx.lineTo(area.right, y); ctx.moveTo(x, area.top); ctx.lineTo(x, area.bottom); ctx.stroke();
          ctx.setLineDash([]); ctx.beginPath(); ctx.arc(x, y, 11, 0, Math.PI * 2); ctx.stroke();
          if (chart.scatterPinnedIndexes?.length) {
            for (const target of scatterProjectionPoints(chart)) {
              const targetX = chart.scales.x.getPixelForValue(target.value);
              const targetY = chart.scales.y.getPixelForValue(target.value);
              const active = target.source === chart.scatterProjectionHover;
              ctx.beginPath(); ctx.arc(targetX, targetY, 4.25, 0, Math.PI * 2);
              ctx.fillStyle = active ? '#fff1c4' : color('--panel');
              ctx.strokeStyle = color('--gold'); ctx.lineWidth = active ? 2.5 : 1.5;
              ctx.fill(); ctx.stroke();
            }
          }
          ctx.restore();
        },
      }, ChartDataLabels],
      data: {labels: [], datasets: [{
        label: 'Teams', data: [], backgroundColor: [], borderColor: color('--panel'), borderWidth: 1.5,
        pointRadius: context => context.chart.scatterPointRadius || 6, pointHitRadius: 6,
        pointHoverRadius: context => (context.chart.scatterPointRadius || 6) + 3,
        pointHoverBorderColor: color('--gold'), pointHoverBorderWidth: 2,
      }]},
    });
    chart.scatterPinnedIndexes = [];
    chart.scatterGuideSource = null;
    chart.scatterShowLogos = scatterLogoToggle.checked;
    keyboardAccess(chart, mark => `${chart.teamRows[mark.index].name}. ${teamDescription(chart, mark.index).join('. ')}`);
    return chart;
  }
  function quadrantPoint(row, expected, units) {
    const values = units === 'total' ? row.totals : row.values;
    const key = expected ? 'expected' : 'actual';
    if (values[0][key] === null || values[1][key] === null) return null;
    return {x: values[1][key], y: values[0][key]};
  }
  function quadrantAverage(rows, expected, units) {
    // Rates weight by appearances; totals compare the mean team total.
    const covered = rows.filter(row => quadrantPoint(row, expected, units) !== null);
    const denominator = covered.reduce((sum, row) => sum + (units === 'total' ? 1 : row.played), 0);
    if (!denominator) return null;
    return covered.reduce((sum, row) => {
      const point = quadrantPoint(row, expected, 'total');
      return {x: sum.x + point.x / denominator, y: sum.y + point.y / denominator};
    }, {x: 0, y: 0});
  }
  function quadrantDescription(chart, index) {
    const row = chart.teamRows[index], suffix = chart.teamUnits === 'total' ? ' total' : ' per match';
    return [false, true].map(expected => {
      const point = quadrantPoint(row, expected, chart.teamUnits), label = expected ? T('xG') : T('Goals');
      return point ? `${label} scored: ${fixed(point.y)} · allowed: ${fixed(point.x)}${suffix}` : T('xG unavailable (incomplete coverage)');
    }).concat(`${row.played} played${chart.quadrantActive ? ' · In progress' : ''}`);
  }
  function clearQuadrantSelection(chart, update = true) {
    if (!chart) return;
    chart.quadrantPinnedIndexes = [];
    root.querySelector('[data-team-quadrant-selection]').hidden = true;
    root.querySelector('[data-team-quadrant-selection-teams]').replaceChildren();
    if (update) clearChart(chart);
  }
  function createTeamQuadrant(canvas) {
    const options = commonOptions();
    options.layout = scatterLayout(34, 34);
    options.interaction.mode = 'point';
    options.onClick = (_, elements, chart) => {
      const indexes = [...new Set(elements.map(element => element.index))];
      clearQuadrantSelection(chart, false);
      chart.quadrantPinnedIndexes = indexes;
      root.querySelector('[data-team-quadrant-selection]').hidden = !indexes.length;
      root.querySelector('[data-team-quadrant-selection-teams]').replaceChildren(...indexes.map(index => {
        const card = document.createElement('article'); card.className = 'explore-scatter-selection-team';
        const heading = document.createElement('h4'); heading.append(teamName(chart.teamRows[index])); card.append(heading);
        quadrantDescription(chart, index).forEach(line => { const p = document.createElement('p'); p.textContent = line; card.append(p); });
        return card;
      }));
      chart.update('none');
    };
    options.scales = Object.fromEntries(['x', 'y'].map(axis => [axis, {
      type: 'linear', reverse: axis === 'x', grid: {color: color('--line')}, border: {display: false},
      ticks: {includeBounds: false, maxTicksLimit: 5}, title: {display: true, text: ''},
    }]));
    options.plugins.tooltip.filter = (_, index) => index < 2;
    options.plugins.tooltip.callbacks = {
      title: () => '',
      label: item => [`${item.chart.teamRows[item.dataIndex].name} · ${item.dataset.label}`, ...quadrantDescription(item.chart, item.dataIndex)],
      footer: items => {
        const count = items[0]?.chart.getActiveElements().length || 0;
        return count > items.length ? '+ More overlapping points; click for all details or use arrow keys' : '';
      },
    };
    options.plugins.datalabels = {
      align: 'top', offset: 8, clamp: true, color: color('--ink'), backgroundColor: color('--panel'),
      borderRadius: 3, padding: 3, font: {size: 11},
      display: context => context.active || (!context.chart.scatterShowLogos && context.chart.quadrantPinnedIndexes?.includes(context.dataIndex)) ? 'auto' : false,
      formatter: (_, context) => context.chart.teamRows?.[context.dataIndex]?.name || '',
    };
    const chart = new Chart(canvas, {
      type: 'scatter', options, data: {labels: [], datasets: [
        {label: T('Goals'), pointStyle: 'circle', backgroundColor: goalsColor},
        {label: T('xG'), pointStyle: 'rectRot', backgroundColor: xgColor},
      ].map(series => ({...series, data: [], borderColor: color('--panel'), borderWidth: 1.5,
        pointRadius: context => context.chart.scatterPointRadius || 6, pointHitRadius: 4,
        pointHoverRadius: context => (context.chart.scatterPointRadius || 6) + 3,
        pointHoverBorderColor: color('--gold'), pointHoverBorderWidth: 2}))},
      plugins: [{id: 'teamQuadrants', afterLayout: fitScatterPoints, beforeDatasetsDraw(chart) {
        const average = chart.quadrantAverage;
        if (!average) return;
        const {left, right, top, bottom} = chart.chartArea;
        const x = chart.scales.x.getPixelForValue(average.x), y = chart.scales.y.getPixelForValue(average.y), ctx = chart.ctx;
        ctx.save();
        ctx.fillStyle = 'rgba(19,111,74,.05)'; ctx.fillRect(x, top, right - x, y - top);
        ctx.fillStyle = 'rgba(91,63,150,.05)'; ctx.fillRect(left, y, x - left, bottom - y);
        ctx.strokeStyle = color('--muted'); ctx.lineWidth = 1.25; ctx.setLineDash([5, 5]);
        ctx.beginPath(); ctx.moveTo(x, top); ctx.lineTo(x, bottom); ctx.moveTo(left, y); ctx.lineTo(right, y); ctx.stroke();
        ctx.setLineDash([]); ctx.fillStyle = color('--muted'); ctx.font = `${chart.width < 400 ? 10 : 12}px system-ui`;
        for (const [align, px, allowance] of [['left', left + 6, 'More allowed'], ['right', right - 6, 'Fewer allowed']]) {
          ctx.textAlign = align;
          // On narrow plots, the direction note and axis titles already explain
          // the upper corners; leave the centered Goals/xG key unobstructed.
          if (chart.width >= 480) {
            ctx.fillText('More scored', px, top - 22); ctx.fillText(allowance, px, top - 8);
          }
          ctx.fillText('Fewer scored', px, chart.height - 22); ctx.fillText(allowance, px, chart.height - 8);
        }
        if (chart.isDatasetVisible(0) && chart.isDatasetVisible(1)) {
          chart.teamRows.forEach((_, index) => {
            const actual = chart.getDatasetMeta(0).data[index], expected = chart.getDatasetMeta(1).data[index];
            if (!actual || !expected || actual.skip || expected.skip) return;
            const selected = chart.quadrantPinnedIndexes?.includes(index) || chart.getActiveElements().some(mark => mark.index === index);
            ctx.strokeStyle = selected ? color('--gold') : color('--muted'); ctx.globalAlpha = selected ? 1 : .4; ctx.lineWidth = selected ? 2.5 : 1;
            ctx.beginPath(); ctx.moveTo(expected.x, expected.y); ctx.lineTo(actual.x, actual.y); ctx.stroke();
          });
        }
        ctx.restore();
      }, afterDatasetsDraw: drawScatterLogos}, ChartDataLabels],
    });
    chart.quadrantPinnedIndexes = [];
    chart.scatterShowLogos = quadrantLogoToggle.checked;
    keyboardAccess(chart, mark => `${chart.teamRows[mark.index].name} · ${chart.data.datasets[mark.datasetIndex].label}. ${quadrantDescription(chart, mark.index).join('. ')}`);
    return chart;
  }
  function showTeamQuadrant(season, units, mode) {
    const rows = sortedTeams(season.teams, teamMeasures.for, 'name', 'asc', units);
    const expectedOnly = mode === 'xg';
    const points = rows.flatMap(row => [
      ...(expectedOnly ? [] : [quadrantPoint(row, false, units)]),
      ...(mode === 'goals' ? [] : [quadrantPoint(row, true, units)]),
    ].filter(Boolean));
    const empty = points.length === 0;
    for (const selector of ['chart-wrap', 'legend', 'logo-control', 'hint']) root.querySelector(`[data-team-quadrant-${selector}]`).hidden = empty;
    root.querySelector('[data-team-quadrant-empty]').hidden = !empty;
    root.querySelector('[data-team-quadrant-average]').hidden = empty;
    root.querySelector('[data-quadrant-goals-key]').hidden = expectedOnly;
    root.querySelector('[data-quadrant-xg-key]').hidden = mode === 'goals';
    root.querySelector('[data-quadrant-connection-key]').hidden = mode !== 'both';
    const missing = rows.filter(row => quadrantPoint(row, true, units) === null).map(row => row.name);
    const note = root.querySelector('[data-team-quadrant-missing]');
    note.hidden = mode === 'goals' || !missing.length;
    note.textContent = T(`Incomplete xG for ${missing.join(', ')}; ${expectedOnly ? 'these teams have no xG point' : 'their goals remain visible, without an xG point or connecting line'}.`);
    if (empty) return;
    const canvas = root.querySelector('[data-chart="team-quadrant"]');
    const chart = charts.teamQuadrant || (charts.teamQuadrant = createTeamQuadrant(canvas));
    clearQuadrantSelection(chart, false);
    chart.teamRows = rows; chart.teamUnits = units; chart.quadrantActive = season.active;
    chart.quadrantAverage = quadrantAverage(rows, expectedOnly, units);
    chart.data.labels = rows.map(row => row.name);
    chart.data.datasets.forEach((series, index) => {
      // Scatter object parsing requires an object even for an unavailable mark.
      series.data = rows.map(row => quadrantPoint(row, index === 1, units) ?? {x: null, y: null});
      chart.setDatasetVisibility(index, index === 0 ? !expectedOnly : mode !== 'goals');
    });
    const average = chart.quadrantAverage, suffix = units === 'total' ? 'in total' : 'per match';
    const domain = plotDomain([...points.flatMap(point => [point.x, point.y]), average.x, average.y]);
    for (const axis of ['x', 'y']) {
      chart.options.scales[axis].min = domain.min;
      chart.options.scales[axis].max = domain.max;
    }
    const label = T(expectedOnly ? 'xG' : mode === 'both' ? 'Goals / xG' : 'Goals');
    chart.options.scales.x.title.text = `${label} allowed ${suffix} · fewer →`;
    chart.options.scales.y.title.text = `${label} scored ${suffix} · more →`;
    root.querySelector('[data-team-quadrant-average]').textContent =
      T(`Dashed lines: ${expectedOnly ? 'covered teams’ xG' : 'league goals'} average — ${fixed(average.y)} scored, ${fixed(average.x)} allowed ${suffix}. ` +
      (units === 'total' ? 'Average team totals; teams may have played different numbers of matches.' : 'Weighted by played matches.') +
      (mode === 'both' ? ' Both series use the goals average as a shared reference.' : ''));
    canvas.setAttribute('aria-label', T(`${season.season} regular season${season.active ? ' (in progress)' : ''}: ${label} scored versus allowed ${suffix}. Up means more scored; right means fewer allowed. Dashed lines show ${expectedOnly ? 'covered teams’ xG' : 'league goals'} averages.`));
    chart.resize(); chart.update('none');
  }
  function showTeams(params) {
    const season = teamSeasons.find(row => row.season === (params.get('season') || root.dataset.defaultTeamSeason));
    teamSeason.value = season?.season || '';
    teamMeasure.value = Object.hasOwn(teamMeasures, params.get('measure')) ? params.get('measure') : 'difference';
    teamUnits.value = params.get('units') === 'total' ? 'total' : 'per-match';
    const units = teamUnits.value;
    const unitLabel = units === 'total' ? 'in total' : 'per match';
    const measure = teamMeasures[teamMeasure.value];
    const isPointsMeasure = measure === teamMeasures.points;
    const rows = season?.teams || [];
    const display = teamDisplays.includes(params.get('display')) ? params.get('display') : teamDisplays[0];
    quadrantData.value = ['goals', 'xg', 'both'].includes(params.get('quadrant-data')) ? params.get('quadrant-data') : 'goals';
    const columns = [...root.querySelectorAll('[data-team-sort]')].map(link => link.dataset.teamSort);
    let requestedSort = params.get('team-sort');
    if (['actual', 'expected', 'gap'].includes(requestedSort)) requestedSort = `${teamMeasure.value}-${requestedSort}`;
    const column = columns.includes(requestedSort) ? requestedSort : 'difference-gap';
    const order = params.get('team-order') === 'asc' ? 'asc' : 'desc';
    root.querySelectorAll('[data-team-display]').forEach(link => {
      link.href = teamLink({display: link.dataset.teamDisplay, 'team-sort': column});
      if (link.dataset.teamDisplay === display) link.setAttribute('aria-current', 'page');
      else link.removeAttribute('aria-current');
    });
    root.querySelector('[data-team-controls] [name="display"]').value = display;
    root.querySelector('[data-team-controls] [name="team-sort"]').value = column;
    root.querySelector('[data-team-controls] [name="team-order"]').value = order;
    root.querySelector('#explore-teams-title').textContent = display === 'gap'
      ? 'Actual − expected by team'
      : display === 'scatter' ? 'Actual vs expected by team'
      : display === 'quadrant' ? 'Scored vs allowed by team' : 'Actual vs expected';
    teamsPanel.querySelector('[data-segmented="measure"]').hidden = display === 'table' || display === 'quadrant';
    teamsPanel.querySelector('[data-penalties-selector]').closest('label').hidden = display !== 'table' && display !== 'quadrant' && isPointsMeasure;
    teamsPanel.querySelector('[data-segmented="quadrant-data"]').hidden = display !== 'quadrant';
    scatterLogoToggle.closest('label').hidden = display !== 'scatter';
    quadrantLogoToggle.closest('label').hidden = display !== 'quadrant';
    root.querySelector('[data-team-units-note]').textContent =
      `${units === 'total' ? 'Regular-season totals' : 'Per regular-season match'}, from recorded results.${display === 'quadrant' ? (season?.active ? ' Season in progress.' : '') : ' Points compare earned points with ASA xPoints from those matches.'}${nonPenalty ? ' Penalty kicks excluded.' : ''}`;
    root.querySelector('[data-team-table-caption]').textContent =
      `${units === 'total' ? 'Totals' : 'Per-match values'}. Gap = actual − expected.`;
    setPointsNote('teams', params);
    root.querySelector('[data-team-plot]').hidden = display !== 'chart';
    root.querySelector('[data-team-gap-plot]').hidden = display !== 'gap';
    root.querySelector('[data-team-scatter-plot]').hidden = display !== 'scatter';
    root.querySelector('[data-team-quadrant-plot]').hidden = display !== 'quadrant';
    root.querySelector('[data-team-table]').hidden = display !== 'table';
    const missingXG = rows.some(row => row.values[0].expected === null);
    const missingXPoints = rows.some(row => row.values[3].expected === null);
    const warningTypes = display === 'quadrant' ? [] : display === 'table'
      ? [missingXG && T('xG'), missingXPoints && 'xPoints'].filter(Boolean)
      : [isPointsMeasure ? missingXPoints && 'xPoints' : missingXG && T('xG')].filter(Boolean);
    const warning = root.querySelector('[data-team-warning]');
    warning.hidden = warningTypes.length === 0;
    warning.textContent = warningTypes.length === 0 ? '' : warningTypes.length === 2
      ? T('Some match xG and xPoints data is missing. Affected comparisons are unavailable.')
      : `Some match ${warningTypes[0]} data is missing. Affected teams have no ${warningTypes[0]} comparison yet.`;
    root.querySelectorAll('[data-team-actual-label]').forEach(label => { label.textContent = measure.actual; });
    root.querySelectorAll('[data-team-xg-label]').forEach(label => { label.textContent = measure.expected; });
    root.querySelector('[data-team-empty]').hidden = rows.length > 0;
    root.querySelector('[data-team-results]').hidden = rows.length === 0;
    root.querySelectorAll('[data-team-sort]').forEach(link => {
      const key = link.dataset.teamSort;
      const selected = key === column;
      link.parentElement.setAttribute('aria-sort', selected ? (order === 'asc' ? 'ascending' : 'descending') : 'none');
      link.href = teamLink({display: 'table', 'team-sort': key, 'team-order': selected ? (order === 'asc' ? 'desc' : 'asc') : key === 'name' ? 'asc' : 'desc'});
    });
    const body = root.querySelector('[data-team-rows]');
    const tableSort = teamSortMetric(column);
    body.replaceChildren(...sortedTeams(rows, tableSort.measure, tableSort.column, order, units).map(row => {
      const tr = document.createElement('tr');
      const values = [row.name, row.played];
      tableMeasures.forEach(metric => {
        const value = teamMetric(row, metric, units);
        values.push(units === 'total' ? String(value.actual) : fixed(value.actual), value.expected === null ? 'Unavailable' : fixed(value.expected),
          value.expected === null ? 'Unavailable' : signed(value.actual - value.expected));
      });
      values.forEach((value, index) => {
        const cell = document.createElement(index === 0 ? 'th' : 'td');
        if (index === 0) { cell.scope = 'row'; cell.append(teamName(row)); }
        else if (index >= 4 && (index - 4) % 3 === 0) { const gap = document.createElement('strong'); gap.textContent = value; cell.append(gap); }
        else cell.textContent = value;
        tr.append(cell);
      });
      return tr;
    }));
    if (!rows.length) return;
    if (display === 'quadrant') { showTeamQuadrant(season, units, quadrantData.value); return; }
    if (display === 'gap') {
      const gapRows = sortedTeams(rows, measure, 'gap', measure.index === 1 ? 'asc' : 'desc', units);
      const gapValues = gapRows.map(row => teamValue(row, measure, 'gap', units));
      const gapEmpty = gapValues.every(value => value === null);
      const missingNames = gapRows.filter((_, index) => gapValues[index] === null).map(row => row.name);
      const missingNote = root.querySelector('[data-team-gap-missing]');
      missingNote.hidden = gapEmpty || missingNames.length === 0;
      missingNote.textContent = missingNames.length ? `Incomplete ${measure.expected} for ${missingNames.join(', ')}; these teams have no gap bar.` : '';
      root.querySelector('[data-team-gap-empty]').textContent = `No teams have complete ${measure.expected} for this measure yet.`;
      root.querySelector('[data-team-gap-empty]').hidden = !gapEmpty;
      root.querySelector('[data-team-gap-legend]').hidden = gapEmpty;
      root.querySelector('[data-team-gap-note]').hidden = gapEmpty;
      root.querySelector('[data-team-gap-chart-wrap]').hidden = gapEmpty;
      root.querySelector('[data-team-gap-hint]').hidden = gapEmpty;
      root.querySelector('[data-team-gap-note]').textContent = measure.index === 1
        ? T(`Bars show goals allowed minus xG allowed ${unitLabel}, from most negative to most positive. Negative means fewer goals conceded than expected.`)
        : `Bars show ${measure.actual.toLowerCase()} minus ${measure.expected} ${unitLabel}, from largest positive gap to largest negative gap.`;
      if (gapEmpty) return;
      const canvas = root.querySelector('[data-chart="team-gap"]');
      canvas.parentElement.style.height = `${gapRows.length * 48 + 70}px`;
      root.querySelector('[data-team-gap-labels]').replaceChildren(...gapRows.map(teamName));
      const chart = charts.teamGap || (charts.teamGap = createTeamGap(canvas));
      chart.teamRows = gapRows; chart.teamMeasure = measure; chart.teamUnits = units;
      chart.data.labels = gapRows.map(row => row.name);
      chart.data.datasets[0].data = gapValues;
      chart.options.scales.x = teamGapXAxis(gapValues.filter(value => value !== null), units);
      canvas.setAttribute('aria-label', `${season.season} regular season: ${measure.actual} minus ${measure.expected} ${unitLabel} per team, sorted by gap`);
      chart.resize(); chart.update('none');
      return;
    }
    if (display === 'scatter') {
      root.querySelector('[data-team-scatter-note]').textContent =
        `One dot per team. Dashed line: actual matches expected. ${scatterMeaning[teamMeasure.value]} ` +
        'Farther from the line means a larger gap.';
      const scatterRows = sortedTeams(rows, measure, 'expected', 'asc', units)
        .filter(row => row.values[measure.index].expected !== null);
      const missingNames = rows.filter(row => row.values[measure.index].expected === null).map(row => row.name);
      const scatterEmpty = scatterRows.length === 0;
      scatterLogoToggle.closest('label').hidden = scatterEmpty;
      const missingNote = root.querySelector('[data-team-scatter-missing]');
      missingNote.hidden = scatterEmpty || missingNames.length === 0;
      missingNote.textContent = missingNames.length ? `Incomplete ${measure.expected} for ${missingNames.join(', ')}; these teams have no point.` : '';
      root.querySelector('[data-team-scatter-empty]').textContent = `No teams have complete ${measure.expected} for this measure yet.`;
      root.querySelector('[data-team-scatter-empty]').hidden = !scatterEmpty;
      root.querySelector('[data-team-scatter-legend]').hidden = scatterEmpty;
      root.querySelector('[data-team-scatter-note]').hidden = scatterEmpty;
      root.querySelector('[data-team-scatter-chart-wrap]').hidden = scatterEmpty;
      root.querySelector('[data-team-scatter-hint]').hidden = scatterEmpty;
      if (scatterEmpty) return;
      const canvas = root.querySelector('[data-chart="team-scatter"]');
      const chart = charts.teamScatter || (charts.teamScatter = createTeamScatter(canvas));
      const domain = scatterDomain(scatterRows, measure, units);
      clearScatterSelection(chart, false);
      chart.teamRows = scatterRows; chart.teamMeasure = measure; chart.teamUnits = units;
      chart.data.labels = scatterRows.map(row => row.name);
      chart.data.datasets[0].data = scatterRows.map(row => ({
        x: teamMetric(row, measure, units).expected, y: teamMetric(row, measure, units).actual,
      }));
      chart.data.datasets[0].backgroundColor = scatterRows.map(row => {
        const gap = teamValue(row, measure, 'gap', units);
        return gap > .005 ? goalsColor : gap < -.005 ? xgColor : color('--muted');
      });
      chart.options.scales.x.min = chart.options.scales.y.min = domain.min;
      chart.options.scales.x.max = chart.options.scales.y.max = domain.max;
      chart.options.scales.x.title.text = `${measure.expected} ${unitLabel}`;
      chart.options.scales.y.title.text = `${measure.actual} ${unitLabel}`;
      canvas.setAttribute('aria-label', `${season.season} regular season: ${measure.actual} versus ${measure.expected} ${unitLabel} per team. Dashed line means actual equals expected.`);
      chart.resize(); chart.update();
      return;
    }
    if (display !== 'chart') return;
    const chartRows = sortedTeams(rows, measure, 'actual', measure.index === 1 ? 'asc' : 'desc', units);
    const canvas = root.querySelector('[data-chart="teams"]');
    canvas.parentElement.style.height = `${chartRows.length * 48 + 70}px`;
    root.querySelector('[data-team-labels]').replaceChildren(...chartRows.map(teamName));
    const chart = charts.teams || (charts.teams = createTeams(canvas));
    chart.teamRows = chartRows; chart.teamMeasure = measure; chart.teamUnits = units;
    chart.data.labels = chartRows.map(row => row.name);
    chart.data.datasets[0].data = chartRows.map(row => teamMetric(row, measure, units).actual);
    chart.data.datasets[1].data = chartRows.map(row => teamMetric(row, measure, units).expected);
    chart.data.datasets[0].label = measure.actual;
    chart.data.datasets[1].label = measure.expected;
    chart.options.scales.x.title.text = units === 'total' ? 'Total' : 'Per match';
    canvas.setAttribute('aria-label', `${season.season} regular season: ${measure.actual} and ${measure.expected} ${unitLabel} per team`);
    chart.resize(); chart.update('none');
  }
  function sortedTeamHistory(rows, params) {
    const links = [...root.querySelectorAll('[data-history-sort]')];
    const column = links.some(link => link.dataset.historySort === params.get('history-sort')) ? params.get('history-sort') : 'season';
    const order = params.get('history-order') === 'asc' ? 'asc' : 'desc';
    root.querySelector('[name="history-sort"]').value = column;
    root.querySelector('[name="history-order"]').value = order;
    links.forEach(link => {
      const key = link.dataset.historySort, selected = key === column;
      link.parentElement.setAttribute('aria-sort', selected ? (order === 'asc' ? 'ascending' : 'descending') : 'none');
      const selection = new URLSearchParams(params);
      selection.set('view', 'team-history'); selection.set('team', historyTeam.value);
      selection.set('history-sort', key); selection.set('history-order', selected && order === 'desc' ? 'asc' : 'desc');
      link.href = `?${selection}`;
    });
    const metric = teamSortMetric(column);
    return [...rows].sort((a, b) => {
      const x = column === 'season' ? Number(a.season) : teamValue(a, metric.measure, metric.column);
      const y = column === 'season' ? Number(b.season) : teamValue(b, metric.measure, metric.column);
      if (x === null || y === null) {
        if (x !== y) return (x === null) - (y === null);
      } else if (x !== y) {
        return (x - y) * (order === 'asc' ? 1 : -1);
      }
      return Number(b.season) - Number(a.season);
    });
  }
  function contextHolderLines(bound, limit = Infinity) {
    if (!bound) return ['Unavailable'];
    const lines = bound.holders.slice(0, limit).map(holder => `${holder.name} · ${holder.season} · ${holder.played} played`);
    if (bound.holders.length > limit) lines.push(`+${bound.holders.length - limit} tied; see League records and season ranges below`);
    return lines;
  }
  function historyInspection(chart, mark, compact = false) {
    const dataset = chart.data.datasets[mark.datasetIndex], year = chart.data.labels[mark.index];
    const series = chart.contextSeries, row = chart.historyRows.find(row => row.season === year);
    const season = series?.seasons.find(row => row.season === year);
    const kind = dataset.contextKind;
    if (kind?.startsWith('record-')) {
      const side = kind.slice(7), bound = series[side];
      const label = series.low.value === series.high.value ? 'high and low' : side;
      return {title: `Record ${label} · ${series.label}`, lines: [
        `${fixed(bound.value)} per match`, `Completed seasons since ${series.since}`,
        ...contextHolderLines(bound, compact ? 4 : Infinity),
      ]};
    }
    const lines = [];
    if (!kind) lines.push(`${chart.historyName}: ${fixed(dataset.data[mark.index])} per match`, `${row.played} played`);
    if (series) {
      for (const side of ['high', 'low']) {
        const bound = season?.[side];
        lines.push(`Season ${side}: ${bound ? `${fixed(bound.value)} per match` : 'unavailable'}`);
        if (bound) lines.push(...contextHolderLines(bound, compact ? 2 : Infinity));
      }
    }
    return {title: `${year}${season?.active || row?.active ? ' (in progress)' : ''} · ${dataset.label}`, lines};
  }
  function wrapHistoryTooltip(chart, lines) {
    const length = Math.max(24, Math.floor((chart.width - 40) / 7));
    return lines.flatMap(line => {
      const wrapped = [''];
      for (const word of line.split(' ')) {
        const last = wrapped.length - 1;
        if (wrapped[last] && wrapped[last].length + word.length + 1 > length) wrapped.push(word);
        else wrapped[last] += `${wrapped[last] ? ' ' : ''}${word}`;
      }
      return wrapped;
    });
  }
  const historyContextPlugin = {
    id: 'historyContext',
    afterEvent(chart, args) {
      if (args.replay || !chart.contextSeries || !['mousemove', 'click', 'touchstart', 'touchmove'].includes(args.event.type)) return;
      const {x, y} = args.event, area = chart.chartArea;
      if (x < area.left || x > area.right || y < area.top || y > area.bottom) return;
      // Record lines can be inspected between year marks, as well as at dots.
      // Dots retain the season's holders when a seasonal bound equals a record.
      if (chart.getActiveElements().some(mark => !chart.data.datasets[mark.datasetIndex].contextKind?.startsWith('record-'))) return;
      const targets = chart.data.datasets.flatMap((dataset, datasetIndex) => {
        if (!dataset.contextKind?.startsWith('record-') || dataset.data[0] === null) return [];
        const distance = Math.abs(chart.scales.y.getPixelForValue(dataset.data[0]) - y);
        return distance <= 6 ? [{datasetIndex, distance}] : [];
      }).sort((a, b) => a.distance - b.distance);
      if (!targets.length) return;
      const index = Math.max(0, Math.min(chart.data.labels.length - 1, Math.round(chart.scales.x.getValueForPixel(x))));
      const active = [{datasetIndex: targets[0].datasetIndex, index}];
      chart.setActiveElements(active);
      chart.tooltip.setActiveElements(active, {x, y});
      args.changed = true;
    },
    afterDatasetsDraw(chart) {
      const series = chart.contextSeries;
      if (!series?.low) return;
      const {ctx, chartArea: area} = chart;
      ctx.save(); ctx.font = '12px system-ui, sans-serif'; ctx.textAlign = 'right';
      const equal = series.low.value === series.high.value;
      const close = !equal && Math.abs(chart.scales.y.getPixelForValue(series.low.value)
        - chart.scales.y.getPixelForValue(series.high.value)) < 22;
      for (const side of equal ? ['high'] : ['high', 'low']) {
        const bound = series[side], label = series.low.value === series.high.value ? 'high/low' : side;
        const y = chart.scales.y.getPixelForValue(bound.value) - 7;
        // Draw the full width even for a team with only one eligible season.
        ctx.strokeStyle = color('--muted'); ctx.lineWidth = 1.5; ctx.setLineDash([2, 5]);
        ctx.beginPath(); ctx.moveTo(area.left, y + 7); ctx.lineTo(area.right, y + 7); ctx.stroke();
        if (close) continue;
        const text = `Record ${label} ${fixed(bound.value)}`;
        ctx.fillStyle = color('--paper'); ctx.fillRect(area.right - ctx.measureText(text).width - 7, y - 12, ctx.measureText(text).width + 8, 16);
        ctx.fillStyle = color('--muted'); ctx.fillText(text, area.right - 3, y);
      }
      if (close) {
        const text = `Record range ${fixed(series.low.value)}–${fixed(series.high.value)}`;
        const y = (chart.scales.y.getPixelForValue(series.low.value)
          + chart.scales.y.getPixelForValue(series.high.value)) / 2 - 7;
        ctx.fillStyle = color('--paper'); ctx.fillRect(area.right - ctx.measureText(text).width - 7, y - 12, ctx.measureText(text).width + 8, 16);
        ctx.fillStyle = color('--muted'); ctx.fillText(text, area.right - 3, y);
      }
      ctx.restore();
    },
  };
  function historyContextDatasets(chart, series) {
    const seasons = new Map(series.seasons.map(row => [row.season, row]));
    const neutral = color('--muted');
    const seasonal = [{
      type: 'bar', label: 'Season high–low range', contextKind: 'season-range',
      data: chart.data.labels.map(year => {
        const season = seasons.get(year);
        return season?.low && season?.high ? [season.low.value, season.high.value] : null;
      }),
      backgroundColor: '#a6bab199', borderColor: neutral, borderWidth: 1,
      borderSkipped: false, categoryPercentage: .75, barPercentage: .75,
      maxBarThickness: 24, minBarLength: 2, order: 2,
    }];
    return seasonal.concat(['low', 'high'].map(side => ({
      label: `Record ${side}`, contextKind: `record-${side}`, keyboardOnce: true,
      data: chart.data.labels.map(() => series[side]?.value ?? null),
      borderColor: neutral, borderWidth: 0,
      pointBackgroundColor: neutral, pointRadius: 0, pointHitRadius: 0, pointHoverRadius: 5, order: 1,
    })));
  }
  function showHistoryChart(key, points, rows, measure, visible, context, domain) {
    const canvas = root.querySelector(`[data-chart="${key}"]`);
    const chart = charts[key] || (charts[key] = createTrend(canvas, points, [historyContextPlugin]));
    chart.historyRows = rows; chart.historyName = rows[0].name; chart.contextSeries = context;
    chart.data.datasets = chart.data.datasets.slice(0, 2);
    setTrendData(chart, points);
    chart.data.datasets[0].label = measure.actual;
    chart.data.datasets[1].label = measure.expected;
    chart.data.datasets.forEach((dataset, index) => {
      dataset.hidden = !visible.includes(index === 0 ? 'goals' : 'xg');
      chart.setDatasetVisibility(index, !dataset.hidden);
    });
    if (context) chart.data.datasets.push(...historyContextDatasets(chart, context));
    // Mixed bars need half a category at each edge so the first/last bars stay full width.
    chart.options.scales.x.offset = Boolean(context);
    chart.options.scales.y = {...chart.options.scales.y, beginAtZero: true, min: domain.min, max: domain.max, title: {display: true, text: 'Per match'}};
    chart.options.plugins.tooltip.callbacks = {
      title: items => items[0] ? wrapHistoryTooltip(chart, [historyInspection(chart, {datasetIndex: items[0].datasetIndex, index: items[0].dataIndex}, true).title]) : [],
      label: item => wrapHistoryTooltip(chart, historyInspection(chart, {datasetIndex: item.datasetIndex, index: item.dataIndex}, true).lines),
    };
    canvas.setAttribute('aria-label', `${rows[0].name}: ${visible.map(key => key === 'goals' ? measure.actual : measure.expected).join(' and ')} per match, by regular season${context ? ', with floating league ranges and completed-season record holders' : ''}`);
    chart.resize(); chart.update();
  }
  function showHistoryContextDetails(seriesList) {
    const textElement = (tag, text) => { const element = document.createElement(tag); element.textContent = text; return element; };
    const bounds = (parent, high, low, prefix) => {
      const dl = document.createElement('dl');
      for (const [side, bound] of [['high', high], ['low', low]]) {
        dl.append(textElement('dt', `${prefix} ${side}`));
        const dd = document.createElement('dd');
        if (bound) {
          dd.append(textElement('strong', `${fixed(bound.value)} per match`));
          const list = document.createElement('ul');
          contextHolderLines(bound).forEach(line => list.append(textElement('li', line))); dd.append(list);
        } else dd.textContent = 'Unavailable';
        dl.append(dd);
      }
      parent.append(dl);
    };
    root.querySelector('[data-history-context-values]').replaceChildren(...seriesList.map(series => {
      const section = document.createElement('section'); section.className = 'explore-context-series';
      section.append(textElement('h4', series.label));
      const note = textElement('p', series.since ? `Completed-season records since ${series.since}` : 'No eligible completed-season records available.');
      note.className = 'note'; section.append(note); bounds(section, series.high, series.low, 'Record');
      series.seasons.filter(row => row.teams).forEach(row => {
        section.append(textElement('h5', `${row.season}${row.active ? ' (in progress)' : ''}`));
        bounds(section, row.high, row.low, 'Season');
      });
      return section;
    }));
  }

  function showTeamHistory(params) {
    historyTeam.value = params.get('team') || root.dataset.defaultHistoryTeam;
    historyMeasure.value = Object.hasOwn(teamMeasures, params.get('measure')) ? params.get('measure') : 'difference';
    const measure = teamMeasures[historyMeasure.value];
    historySeries.value = ['goals', 'xg', 'both'].includes(params.get('series')) ? params.get('series') : 'both';
    historyContext.checked = params.get('context') === 'on';
    const contextEnabled = historyContext.checked;
    const visible = historySeries.value === 'both' ? ['goals', 'xg'] : [historySeries.value];
    const context = historyContextData[measure.index];
    const isPoints = measure === teamMeasures.points;
    historyPanel.querySelector('[data-penalties-selector]').closest('label').hidden = isPoints;
    root.querySelector('#explore-team-history-title').textContent =
      `Season-by-season ${isPoints ? 'points' : 'scoring'}`;
    const choices = historyPanel.querySelectorAll('[data-segmented="series"] label span');
    choices[0].textContent = isPoints ? 'Points and xPoints' : 'Goals and xG';
    choices[1].textContent = isPoints ? 'Points' : 'Goals';
    choices[2].textContent = isPoints ? 'xPoints' : 'xG';
    setPointsNote('team-history', params);
    root.querySelectorAll('[data-history-key]').forEach(key => { key.hidden = !visible.includes(key.dataset.historyKey); });
    root.querySelector('[data-history-context-details]').hidden = !contextEnabled;
    root.querySelector('[data-history-context-legend]').hidden = !contextEnabled;
    root.querySelector('[data-history-context-note]').hidden = !contextEnabled;
    showHistoryContextDetails(visible.map(key => context[key]));
    const notes = visible.map(key => context[key].since ? `${context[key].label} records since ${context[key].since}` : `${context[key].label}: no completed-season records`);
    if (visible.includes('xg') && context.xg.partial) notes.push(`Some season ${isPoints ? 'xPoints' : T('xG')} ranges are unavailable because team coverage is incomplete`);
    root.querySelector('[data-history-context-note]').textContent = notes.join('. ') + '.';
    const rows = teamSeasons.flatMap(season => {
      const team = (season.teams || []).find(row => row.id === historyTeam.value);
      return team ? [{...team, season: season.season, active: season.active}] : [];
    });
    root.querySelector('[data-team-history-empty]').hidden = rows.length > 0;
    root.querySelector('[data-team-history-results]').hidden = rows.length === 0;
    const missingXG = rows.some(row => row.values[0].expected === null);
    const missingXPoints = rows.some(row => row.values[3].expected === null);
    const warning = root.querySelector('[data-team-history-warning]');
    warning.hidden = !missingXG && !missingXPoints;
    warning.textContent = missingXG && missingXPoints
      ? T('Some seasons have incomplete xG and xPoints for this team; actual values remain available.')
      : missingXPoints
        ? 'Some seasons have incomplete xPoints for this team; earned points remain available.'
        : T('Some seasons have incomplete xG for this team; goals remain available.');
    root.querySelector('[data-history-team-name]').replaceChildren(...(rows.length ? [teamName(rows[0])] : []));
    root.querySelector('[data-history-actual-label]').textContent = measure.actual;
    root.querySelector('[data-history-xg-label]').textContent = measure.expected;
    root.querySelector('[data-team-history-rows]').replaceChildren(...sortedTeamHistory(rows, params).map(row => {
      const tr = document.createElement('tr');
      const values = [row.season, row.played];
      [2, 0, 1, 3].forEach(index => {
        const value = row.values[index];
        values.push(fixed(value.actual), value.expected === null ? 'Unavailable' : fixed(value.expected));
      });
      values.forEach((value, index) => {
        const cell = document.createElement(index === 0 ? 'th' : 'td');
        if (index === 0) cell.scope = 'row';
        cell.textContent = value;
        tr.append(cell);
      });
      return tr;
    }));
    root.querySelector('[data-team-history-plot]').hidden = rows.length === 0;
    if (!rows.length) return;
    const points = rows.toReversed().map(row => ({
      season: row.season, active: row.active, played: row.played,
      goals: row.values[measure.index].actual, xg: row.values[measure.index].expected,
    }));
    const split = contextEnabled && visible.length === 2;
    root.querySelector('[data-history-xg-panel]').hidden = !split;
    root.querySelector('[data-history-panel-label]').hidden = !split;
    root.querySelector('[data-history-panel-label]').textContent = measure.actual;
    root.querySelector('[data-history-xg-panel-label]').textContent = measure.expected;
    root.querySelector('[data-history-xg-empty]').hidden = !visible.includes('xg') || points.some(row => row.xg !== null);
    root.querySelector('[data-history-xg-empty]').textContent =
      `This team has no complete ${measure.expected} values in these seasons.`;
    const values = points.flatMap(row => visible.map(key => row[key])).filter(value => value !== null);
    if (contextEnabled) {
      visible.forEach(key => {
        const series = context[key];
        values.push(...[series.low?.value, series.high?.value].filter(value => value != null));
        series.seasons.filter(row => row.season >= points[0].season && row.season <= points.at(-1).season).forEach(row => {
          values.push(...[row.low?.value, row.high?.value].filter(value => value != null));
        });
      });
    }
    const low = Math.min(0, ...values), high = Math.max(0, ...values), pad = (high - low) * .15 || .25;
    const domain = {min: low < 0 ? low - pad : 0, max: high + pad};
    showHistoryChart('team-history', points, rows, measure, split ? ['goals'] : visible, contextEnabled ? context[visible[0]] : null, domain);
    if (split) showHistoryChart('team-history-xg', points, rows, measure, ['xg'], context.xg, domain);
  }
  // The table holds both bases' rows; bars scale to the shown basis.
  function styleTable() {
    const cells = [...root.querySelectorAll('.explore-rate')].filter(cell => cell.closest('tr').dataset.basisOnly === root.dataset.basis);
    const maximum = Math.max(1, ...cells.map(cell => Number(cell.dataset.value)));
    cells.forEach(cell => {
      if (cell.dataset.value !== '') cell.style.setProperty('--rate-width', String(Number(cell.dataset.value) / maximum));
    });
    root.querySelectorAll('.explore-gap').forEach(cell => {
      if (cell.dataset.value === '' || cell.dataset.direction) return;
      const value = Number(cell.querySelector('span').textContent);
      cell.dataset.direction = value > 0 ? 'positive' : value < 0 ? 'negative' : 'zero';
      if (value > 0) cell.querySelector('span').textContent = `+${cell.querySelector('span').textContent}`;
      if (value === 0) cell.querySelector('span').textContent = '0.00';
    });
  }
  function sortTable(column, direction) {
    const body = root.querySelector('[data-panel="table"] tbody');
    if (!body) return;
    const rows = Array.from(body.rows);
    rows.sort((a, b) => {
      const x = a.cells[column].dataset.value;
      const y = b.cells[column].dataset.value;
      if (x === '' || y === '') return (x === '') - (y === '');
      return (Number(x) - Number(y)) * (direction === 'asc' ? 1 : -1)
        || Number(a.cells[0].dataset.value) - Number(b.cells[0].dataset.value);
    });
    body.append(...rows);
    root.querySelectorAll('[data-sort]').forEach(button => {
      const selected = Number(button.dataset.sort) === column;
      button.parentElement.setAttribute('aria-sort', selected ? (direction === 'asc' ? 'ascending' : 'descending') : 'none');
    });
  }
  function sortDistributionTable(params) {
    const body = root.querySelector('[data-distribution-rows]');
    if (!body) return;
    const links = [...root.querySelectorAll('[data-distribution-sort]')];
    const keys = links.map(link => link.dataset.distributionSort);
    const requested = params.get('distribution-sort');
    const column = Math.max(0, keys.indexOf(requested));
    const direction = params.get('distribution-order') === 'asc' ? 'asc' : 'desc';
    const rows = [...body.rows];
    rows.sort((a, b) => {
      let comparison;
      if (column < 2) {
        comparison = Number(a.cells[column].dataset.value) - Number(b.cells[column].dataset.value);
      } else {
        comparison = Number(a.cells[column].dataset.value) * Number(b.dataset.matches)
          - Number(b.cells[column].dataset.value) * Number(a.dataset.matches);
      }
      return comparison * (direction === 'asc' ? 1 : -1)
        || Number(b.cells[0].dataset.value) - Number(a.cells[0].dataset.value);
    });
    body.append(...rows);
    links.forEach((link, index) => {
      const selected = index === column;
      link.parentElement.setAttribute('aria-sort', selected ? (direction === 'asc' ? 'ascending' : 'descending') : 'none');
      const selection = new URLSearchParams(params);
      selection.set('view', 'distribution');
      selection.set('distribution-sort', link.dataset.distributionSort);
      selection.set('distribution-order', selected && direction === 'desc' ? 'asc' : 'desc');
      link.href = `?${selection}`;
    });
  }
  function sortHomeTable(params) {
    const body = root.querySelector('[data-home-rows]');
    if (!body) return;
    const links = [...root.querySelectorAll('[data-home-sort]')];
    const column = Math.max(0, links.findIndex(link => link.dataset.homeSort === params.get('home-sort')));
    const direction = params.get('home-order') === 'asc' ? 'asc' : 'desc';
    const rows = [...body.rows];
    rows.sort((a, b) => {
      const x = a.cells[column].dataset.value, y = b.cells[column].dataset.value;
      if (x === '' || y === '') return (x === '') - (y === '');
      return (Number(x) - Number(y)) * (direction === 'asc' ? 1 : -1)
        || Number(b.cells[0].dataset.value) - Number(a.cells[0].dataset.value);
    });
    body.append(...rows);
    links.forEach((link, index) => {
      const selected = index === column;
      link.parentElement.setAttribute('aria-sort', selected ? (direction === 'asc' ? 'ascending' : 'descending') : 'none');
      const selection = new URLSearchParams(params);
      selection.set('view', 'home-advantage');
      selection.set('home-sort', link.dataset.homeSort);
      selection.set('home-order', selected && direction === 'desc' ? 'asc' : 'desc');
      link.href = `?${selection}`;
    });
  }
  function sortParityTable(params) {
    const body = root.querySelector('[data-parity-rows]');
    if (!body) return;
    const links = [...root.querySelectorAll('[data-parity-sort]')];
    const column = Math.max(0, links.findIndex(link => link.dataset.paritySort === params.get('parity-sort')));
    const direction = params.get('parity-order') === 'asc' ? 'asc' : 'desc';
    const rows = [...body.rows];
    rows.sort((a, b) => {
      const x = a.cells[column].dataset.value, y = b.cells[column].dataset.value;
      if (x === '' || y === '') return (x === '') - (y === '');
      return (Number(x) - Number(y)) * (direction === 'asc' ? 1 : -1)
        || Number(b.cells[0].dataset.value) - Number(a.cells[0].dataset.value);
    });
    body.append(...rows);
    const selection = new URLSearchParams(params);
    selection.set('view', 'parity');
    links.forEach((link, index) => {
      const selected = index === column;
      link.parentElement.setAttribute('aria-sort', selected ? (direction === 'asc' ? 'ascending' : 'descending') : 'none');
      const target = new URLSearchParams(selection);
      target.set('parity-sort', link.dataset.paritySort);
      target.set('parity-order', selected && direction === 'desc' ? 'asc' : 'desc');
      link.href = `?${target}`;
    });
    const year = paritySeason(params);
    root.querySelectorAll('[data-parity-season-link]').forEach(link => {
      const target = new URLSearchParams(selection);
      target.set('season', link.dataset.paritySeasonLink);
      link.href = `?${target}`;
      if (link.dataset.paritySeasonLink === year) link.setAttribute('aria-current', 'true'); else link.removeAttribute('aria-current');
    });
  }
  function rollingMatchValue(matches, end, measure, window, expected) {
    window = Math.min(end + 1, window);
    let average = 0;
    for (let index = end + 1 - window; index <= end; index++) {
      const metric = matches[index].values[measure];
      const value = expected ? metric.expected : metric.actual;
      if (value == null) return null;
      average += value / window;
    }
    return average;
  }
  // Mirrors opponentAdjusted in Go: goals and xG divided by the match's
  // opponent factors; points and xPoints shifted by how many more points an
  // average team would expect here than in the season's average match. The
  // official match and that average ride along for tooltips.
  function opponentAdjusted(match, averagePoints) {
    const factors = match.opponents, values = [...match.values];
    if (!factors) {
      [0, 1, 2].forEach(measure => { values[measure] = {actual: 0, expected: null}; });
      values[3] = {actual: null, expected: null};
      return {...match, goalsCovered: false, values, official: match, averagePoints};
    }
    const shift = factors.expectedPoints - averagePoints, points = match.values[3];
    values[3] = {actual: points.actual - shift, expected: points.expected == null ? null : points.expected - shift};
    const divide = (value, factor) => ({actual: value.actual / factor, expected: value.expected == null ? null : value.expected / factor});
    values[0] = divide(match.values[0], factors.scored);
    values[1] = divide(match.values[1], factors.allowed);
    values[2] = {actual: values[0].actual - values[1].actual, expected: values[0].expected == null || values[1].expected == null ? null : values[0].expected - values[1].expected};
    return {...match, values, official: match, averagePoints};
  }
  // "Goals scored" becomes "Adjusted goals scored"; xG and npxG keep their case.
  const adjustedLabel = label => `Adjusted ${/^(np)?xG/.test(label) ? label : label[0].toLowerCase() + label.slice(1)}`;
  // The view registry's rule, for a season with opponent ratings.
  function trendOpponentsShown(params) {
    const view = views.get('season-trend');
    const season = teamSeasons.find(row => row.season === (params.get('season') || root.dataset.defaultTeamSeason));
    return Boolean(season?.opponents) && ruleMatches(view.opponentAdjustment, params, view.defaults);
  }
  const trendAdjusted = params => trendOpponentsShown(params) && params.get('opponents') === 'adjusted';
  function opponentFactorLines(row) {
    if (!row.official) return [];
    const factor = value => row.opponents ? value.toFixed(2) : 'Unavailable';
    return [`Opponent defense × venue × rest: ${factor(row.opponents?.scored)}`, `Opponent attack × venue × rest: ${factor(row.opponents?.allowed)}`];
  }
  function seasonTrendValue(matches, end, measure, window, expected, rolling) {
    if (rolling) return rollingMatchValue(matches, end, measure, window, expected);
    const metric = matches[end].values[measure];
    return expected ? metric.expected : metric.actual;
  }
  function seasonMatchMean(matches, measure, expected) {
    const values = matches.map(match => expected ? match.values[measure].expected : match.values[measure].actual).filter(value => value != null);
    return {value: values.length ? values.reduce((total, value) => total + value, 0) / values.length : null, count: values.length, total: matches.length};
  }
  function trendReferences(season, matches, measure, expected, reference) {
    const own = {...seasonMatchMean(matches, measure, expected), label: 'Team average', shortLabel: 'Team', note: 'All recorded matches for this team; expected means use available observations.', names: []};
    const benchmark = season.benchmarks?.find(row => row.key === reference);
    if (!benchmark) return [own];
    const shortLabel = {league: 'League', playoff: 'Playoffs', 'top-four': 'Home playoff', shield: 'Shield', best: 'Best'}[reference];
    return [own, {...benchmark.means[measure][expected ? 1 : 0], label: benchmark.label, shortLabel, note: benchmark.note, identifyTeams: reference === 'shield' || reference === 'best'}];
  }
  // Reference lines end in a leader and a label to the right of the plot.
  const referenceLabelFont = '11px sans-serif';
  const referenceLabel = reference => `${reference.shortLabel} ${fixed(reference.value)}`;
  // The right padding fits the widest label after its 14px leader, plus a margin.
  function referenceLabelGutter(chart) {
    const ctx = chart.ctx;
    ctx.save(); ctx.font = referenceLabelFont;
    const widest = Math.max(0, ...chart.trendPlotReferences.filter(reference => reference.value != null).map(reference => ctx.measureText(referenceLabel(reference)).width));
    ctx.restore();
    return Math.ceil(14 + widest + 6);
  }
  function seasonTrendNumber(value, expected, rolling, signedValue = false) {
    if (value == null) return 'Unavailable';
    if (expected || rolling) return signedValue ? signed(value) : fixed(value);
    return `${signedValue && value > 0 ? '+' : ''}${value}`;
  }
  function seasonTrendInspection(chart, mark) {
    const match = chart.matchRows[mark.index];
    const title = `Match ${mark.index + 1} · ${match.date} · ${match.venue} vs ${match.opponent} · ${match.score} · ${{3: 'Win', 1: 'Draw', 0: 'Loss'}[match.values[3].actual]}`;
    const lines = chart.data.datasets.map(series => {
      const value = series.data[mark.index];
      const official = series.raw ? ` · official ${seasonTrendNumber(series.raw[mark.index], series.expected, chart.trendRolling, chart.trendSigned)}` : '';
      return `${series.label}: ${seasonTrendNumber(value, series.expected, chart.trendRolling, chart.trendSigned)}${chart.trendRolling && value != null ? ' per match' : ''}${official}`;
    });
    lines.push(...opponentFactorLines(match));
    if (chart.trendAverage) chart.data.datasets.forEach(series => series.references.forEach(reference => {
      const names = reference.identifyTeams && reference.names?.length ? ` · ${reference.names.join(', ')}` : '';
      lines.push(`${reference.label}${names}: ${reference.value == null ? 'Unavailable' : fixed(reference.value) + ' per match'}`);
    }));
    if (chart.trendRolling) lines.unshift(mark.index + 1 < chart.trendWindow
      ? `Average over ${mark.index + 1} ${mark.index === 0 ? 'match' : 'matches'} · ${chart.trendWindow}-match window`
      : `Matches ${mark.index + 2 - chart.trendWindow}–${mark.index + 1} (including this match)`);
    return {title, lines};
  }
  function createSeasonTrend(canvas) {
    const options = commonOptions();
    options.interaction = {mode: 'index', axis: 'x', intersect: false};
    options.scales = {
      x: {offset: false, grid: {display: false}, title: {display: true, text: 'Team match number'}, ticks: {maxRotation: 0, maxTicksLimit: 10}},
      y: {beginAtZero: true, title: {display: true, text: 'Per match'}, grid: {color: color('--line')}},
    };
    options.plugins.datalabels = {display: false};
    options.plugins.tooltip.callbacks = {
      title: items => items.length ? wrapHistoryTooltip(items[0].chart, [seasonTrendInspection(items[0].chart, {index: items[0].dataIndex}).title]) : [],
      beforeBody: items => items.length ? wrapHistoryTooltip(items[0].chart, seasonTrendInspection(items[0].chart, {index: items[0].dataIndex}).lines) : [],
      label: () => [],
    };
    // Drawn before the tooltip, which must stay on top.
    const guide = {id: 'seasonMatchGuide', afterDatasetsDraw(chart) {
      const mark = chart.getActiveElements()[0];
      if (!mark || !chart.chartArea) return;
      const x = chart.scales.x.getPixelForValue(mark.index);
      const {top, bottom} = chart.chartArea, ctx = chart.ctx;
      ctx.save(); ctx.strokeStyle = color('--muted'); ctx.lineWidth = 1;
      ctx.beginPath(); ctx.moveTo(x, top); ctx.lineTo(x, bottom); ctx.stroke(); ctx.restore();
    }};
    const averages = {id: 'seasonAverageReferences', beforeDatasetsDraw(chart) {
      if (!chart.trendAverage || !chart.chartArea) return;
      const {left, right} = chart.chartArea, ctx = chart.ctx;
      ctx.save(); ctx.lineWidth = 1.25; ctx.setLineDash([1, 4]); ctx.lineCap = 'round'; ctx.globalAlpha = .75;
      chart.trendPlotReferences.forEach(reference => {
        if (reference.value == null) return;
        const y = chart.scales.y.getPixelForValue(reference.value);
        ctx.strokeStyle = reference.ink;
        ctx.beginPath(); ctx.moveTo(left, y); ctx.lineTo(right, y); ctx.stroke();
      });
      ctx.restore();
    }, afterDatasetsDraw(chart) {
      if (!chart.trendAverage || !chart.chartArea) return;
      const {right, top, bottom} = chart.chartArea, ctx = chart.ctx;
      const references = chart.trendPlotReferences.filter(reference => reference.value != null)
        .map(reference => ({...reference, y: chart.scales.y.getPixelForValue(reference.value)})).sort((a, b) => a.y - b.y);
      references.forEach((reference, index) => {
        reference.labelY = Math.max(top + 7, Math.min(bottom - 7, reference.y));
        if (index) reference.labelY = Math.max(reference.labelY, references[index - 1].labelY + 18);
      });
      for (let index = references.length - 1; index >= 0; index--) {
        references[index].labelY = Math.min(references[index].labelY, bottom - 7 - (references.length - 1 - index) * 18);
      }
      ctx.save(); ctx.font = referenceLabelFont; ctx.textAlign = 'left'; ctx.textBaseline = 'middle';
      references.forEach(({ink, y, labelY, shortLabel, value}) => {
        ctx.strokeStyle = ink; ctx.lineWidth = 1;
        ctx.beginPath(); ctx.moveTo(right, y); ctx.lineTo(right + 5, y); ctx.lineTo(right + 10, labelY); ctx.stroke();
        ctx.fillStyle = ink;
        ctx.fillText(referenceLabel({shortLabel, value}), right + 14, labelY);
      });
      ctx.restore();
    }};
    const chart = new Chart(canvas, {type: 'line', data: {labels: [], datasets: []}, options, plugins: [averages, guide]});
    chart.keyboardByIndex = true;
    keyboardAccess(chart, mark => {
      const inspection = seasonTrendInspection(chart, mark);
      return `${inspection.title}. ${inspection.lines.join('. ')}`;
    });
    return chart;
  }
  // One Smoothing control: none, 3, 5 or 10. Old trend-mode/window history
  // entries still map onto it; the server redirects such links.
  function trendSmoothing(params) {
    if (['none', '3', '5', '10'].includes(params.get('smoothing'))) return params.get('smoothing');
    if (params.get('trend-mode') === 'match') return 'none';
    return ['3', '5', '10'].includes(params.get('window')) ? params.get('window') : '5';
  }
  function trendSortAvailable(params) {
    const key = params.get('trend-sort') || 'match', view = params.get('trend-view') || 'balance';
    return ['match', 'venue', 'result'].includes(key) ||
      (trendAdjusted(params) && view !== 'points' && ['adjusted-goals-for', 'adjusted-goals-against', 'adjusted-xg-for', 'adjusted-xg-against'].includes(key)) ||
      (trendAdjusted(params) && view === 'points' && ['adjusted-points', 'adjusted-xpoints'].includes(key)) ||
      (view === 'points' && key === 'points-gap') ||
      (view === 'compare' && ['scored-gap', 'allowed-gap'].includes(key)) ||
      (view === 'difference' && ['goal-margin', 'xg-margin'].includes(key));
  }
  function showSeasonTrendTable(params, matches, series, {view, rolling, window, adjustedRows}) {
    const metric = (label, measure, expected = false, key = '', gap = false) => ({label, key, gap,
      value: row => gap ? measure === 3 ? row.values[3].expected == null ? null : row.values[3].actual - row.values[3].expected : globalThis.NWSLGeometry.goalGap(marginsData.goals(row), marginsData.xg(row), measure) : expected ? row.values[measure].expected : measure !== 3 && !row.goalsCovered ? null : row.values[measure].actual,
      format: value => seasonTrendNumber(value, expected || gap, false, gap),
    });
    const columns = [
      {label: 'Match', key: 'match', value: (_, index) => index + 1},
      {label: 'Date (venue local)', value: row => row.date},
      {label: 'Venue', key: 'venue', value: row => row.venue},
      {label: 'Opponent', value: row => row.opponent},
      {label: 'Score', value: row => row.score},
      {label: 'Result', key: 'result', value: row => row.values[3].actual, format: value => ({3: 'Win', 1: 'Draw', 0: 'Loss'}[value])},
      metric('Points', 3),
      ...(nonPenalty ? [{label: 'Non-penalty goals', value: row => row.goalsCovered ? marginsData.goals(row).join('–') : 'Unavailable'}] : []),
      metric(T('xG for'), 0, true), metric(T('xG against'), 1, true), metric('xPoints', 3, true),
    ];
    // Adjusted columns read the opponent-adjusted rows; the rest read official.
    const adjustedMetric = (label, measure, expected, key) => ({label, key, adjusted: true,
      value: (_, index) => expected || measure === 3 || adjustedRows[index].goalsCovered ? adjustedRows[index].values[measure][expected ? 'expected' : 'actual'] : null,
      format: value => seasonTrendNumber(value, true, false),
    });
    if (adjustedRows !== matches && view === 'points') columns.push(adjustedMetric('Adjusted points', 3, false, 'adjusted-points'), adjustedMetric('Adjusted xPoints', 3, true, 'adjusted-xpoints'));
    else if (adjustedRows !== matches) columns.push(
      adjustedMetric(`Adjusted ${nonPenalty ? 'non-penalty goals' : 'goals'} for`, 0, false, 'adjusted-goals-for'),
      adjustedMetric(`Adjusted ${nonPenalty ? 'non-penalty goals' : 'goals'} against`, 1, false, 'adjusted-goals-against'),
      adjustedMetric(T('Adjusted xG for'), 0, true, 'adjusted-xg-for'), adjustedMetric(T('Adjusted xG against'), 1, true, 'adjusted-xg-against'));
    if (view === 'difference') columns.push(metric(T('Goal margin'), 2, false, 'goal-margin'), metric(T('xG margin'), 2, true, 'xg-margin'));
    if (view === 'compare') columns.push(metric(T('Goals − xG'), 0, false, 'scored-gap', true), metric(T('Goals allowed − xG allowed'), 1, false, 'allowed-gap', true));
    if (view === 'points') columns.push(metric('Points − xPoints', 3, false, 'points-gap', true));
    if (rolling) series.forEach(dataset => columns.push({label: `${dataset.label} · ${window}-match average`, value: (_, index) => {
      if (!dataset.expected && dataset.measure !== 3 && adjustedRows.slice(Math.max(0, index + 1 - window), index + 1).some(row => !row.goalsCovered)) return null;
      return dataset.data[index];
    }, format: value => seasonTrendNumber(value, dataset.expected, true)}));
    const requested = params.get('trend-sort') || 'match';
    const sorted = columns.find(column => column.key === requested) || columns[0];
    const key = sorted.key, desc = key === requested && params.get('trend-order') === 'desc';
    const expanded = params.has('trend-sort') || params.has('trend-order');
    root.querySelector('[data-trend-values]').open = expanded;
    root.querySelector('[data-trend-headings]').replaceChildren(...columns.map(column => {
      const heading = archiveElement('th', column.label); heading.scope = 'col';
      if (column.key) {
        const active = key === column.key, target = new URLSearchParams(params);
        if (target.get('penalties') === 'include') target.delete('penalties');
        const nextDesc = active ? !desc : column.gap || column.adjusted || ['goal-margin', 'xg-margin'].includes(column.key);
        target.set('trend-sort', column.key); target.delete('trend-order');
        if (nextDesc) target.set('trend-order', 'desc');
        heading.setAttribute('aria-sort', active ? desc ? 'descending' : 'ascending' : 'none');
        const link = archiveElement('a', column.label); link.href = `?${target}`; link.dataset.trendSort = column.key;
        link.setAttribute('aria-label', `Sort by ${column.label}`); heading.replaceChildren(link);
      }
      return heading;
    }));
    root.querySelector('[data-trend-table-caption]').textContent = `${rolling ? `Individual match values, followed by ${window}-match trailing averages, per match; matches 1–${window - 1} use all matches played so far` : 'Individual match values'}. Scores are shown from this team's perspective.`;
    const indices = matches.map((_, index) => index);
    indices.sort((a, b) => {
      if (!!matches[a].undated !== !!matches[b].undated) return matches[a].undated ? 1 : -1;
      const x = sorted.value(matches[a], a), y = sorted.value(matches[b], b);
      if (x == null || y == null) {
        if (x != null) return -1;
        if (y != null) return 1;
      } else {
        const comparison = typeof x === 'string' ? x.localeCompare(y) : x - y;
        if (comparison) return comparison * (desc ? -1 : 1);
      }
      return b - a;
    });
    for (const name of ['trend-sort', 'trend-order']) {
      const input = trendControls.elements.namedItem(name);
      input.value = name === 'trend-sort' ? key : desc ? 'desc' : 'asc';
      input.disabled = name === 'trend-sort' ? key === 'match' : !desc;
    }
    root.querySelector('[data-trend-rows]').replaceChildren(...indices.map(index => {
      const row = document.createElement('tr');
      columns.forEach((column, position) => {
        const value = column.value(matches[index], index);
        const cell = archiveElement(position ? 'td' : 'th', column.format ? column.format(value) : String(value));
        if (!position) cell.scope = 'row'; row.append(cell);
      });
      return row;
    }));
  }
  function showSeasonTrend(params) {
    const control = key => trendControls.elements.namedItem(key);
    const season = params.get('season') || root.dataset.defaultTeamSeason;
    const teamID = params.get('team') || root.dataset.defaultHistoryTeam;
    const view = ['balance', 'compare', 'difference', 'points', 'relative'].includes(params.get('trend-view')) ? params.get('trend-view') : 'balance';
    const series = ['goals', 'xg', 'both'].includes(params.get('series')) ? params.get('series') : 'both';
    const smoothing = trendSmoothing(params);
    const mode = smoothing === 'none' ? 'match' : 'rolling';
    const window = smoothing === 'none' ? 5 : Number(smoothing);
    const rolling = mode === 'rolling', difference = view === 'difference', balance = view === 'balance', relative = view === 'relative', points = view === 'points';
    const pointsGap = points && !rolling, margins = difference && !rolling, goalGaps = view === 'compare' && !rolling, balanceBars = balance && !rolling;
    const signedValues = difference;
    const averageSeries = ['goals', 'xg', 'points', 'xpoints'].includes(params.get('average-series')) ? params.get('average-series') : 'goals';
    const averageReference = ['team', 'league', 'playoff', 'top-four', 'shield', 'best'].includes(params.get('average-reference')) ? params.get('average-reference') : 'league';
    const effectiveSeries = relative ? averageSeries : 'both';
    trendPanel.querySelector('[data-penalties-selector]').closest('label').hidden = points || (relative && ['points', 'xpoints'].includes(averageSeries));
    Object.entries({season, team: teamID, 'trend-view': view, series, 'average-series': averageSeries, 'average-reference': averageReference, smoothing}).forEach(([key, value]) => { control(key).value = value; });
    root.querySelector('[data-trend-average-data-control]').hidden = !relative;
    root.querySelector('[data-trend-reference-control]').hidden = !relative;
    root.querySelector('[data-trend-reference-key]').dataset.series = ['xg', 'xpoints'].includes(averageSeries) ? 'xg' : 'goals';
    // Elsewhere a carried opponents value is ignored but kept, as series is.
    const opponentsShown = trendOpponentsShown(params);
    root.querySelector('[data-trend-opponents-control]').hidden = !opponentsShown;
    control('opponents').checked = params.get('opponents') === 'adjusted';
    const adjusted = trendAdjusted(params);

    const selectedSeason = teamSeasons.find(row => row.season === season);
    const comparison = root.querySelector('[data-trend-reference]');
    comparison.replaceChildren(...[{key: 'team', label: 'None (team average only)'}, ...(selectedSeason?.benchmarks || [])].map(reference => {
      const option = document.createElement('option'); option.value = reference.key; option.textContent = reference.label; return option;
    }));
    comparison.value = averageReference;
    const team = selectedSeason?.teams?.find(row => row.id === teamID);
    const matches = team?.matches || [];
    root.querySelector('[data-season-trend-empty]').hidden = matches.length > 0;
    root.querySelector('[data-season-trend-results]').hidden = matches.length === 0;
    if (!matches.length) return;
    const shown = adjusted ? matches.map(match => opponentAdjusted(match, selectedSeason.opponents.averagePoints)) : matches;
    root.querySelector('[data-trend-team-name]').replaceChildren(teamName(team));
    root.querySelector('[data-trend-season-summary]').textContent = `${season} regular season${selectedSeason.active ? ' · In progress' : ''} · ${matches.length} played`;
    root.querySelector('[data-trend-basis="non-penalty"]').hidden = !nonPenalty;
    root.querySelector('[data-trend-basis="opponents"]').hidden = !adjusted;
    // The note carries only what the controls, status line and panel titles do not already say.
    const missingExpected = relative && matches.some(match => averageSeries === 'xg' ? match.values[0].expected == null || match.values[1].expected == null : averageSeries === 'xpoints' && match.values[3].expected == null);
    const note = [rolling ? `Each point averages the last ${window} matches, including that one.` : '',
      relative ? 'Reference lines show the team’s season average and the selected benchmark.' : '',
      missingExpected ? 'Expected means use available matches.' : '',
      points ? 'xPoints: ASA’s expected points for each match.' : '',
      // Shown with the switch, on or off, so toggling never reflows the note.
      opponentsShown ? 'Adjust for opponents rescales values to an average opponent at a neutral venue with even rest.' : '',
      opponentsShown && selectedSeason.opponents.xgMatches < selectedSeason.opponents.completed ? `Opponent ratings use ${selectedSeason.opponents.xgMatches} of ${selectedSeason.opponents.completed} matches with xG.` : ''].filter(Boolean).join(' ');
    root.querySelector('[data-season-trend-note]').textContent = note;
    root.querySelector('[data-season-trend-note]').hidden = !note;
    const pointsMetric = points || ['points', 'xpoints'].includes(effectiveSeries);
    root.querySelector('[data-trend-warning]').hidden = pointsMetric || effectiveSeries === 'goals' || !matches.some(match => match.values[0].expected == null);
    root.querySelector('[data-trend-points-warning]').hidden = !pointsMetric || effectiveSeries === 'points' || !matches.some(match => match.values[3].expected == null);
    root.querySelector('[data-trend-venue-date-unavailable]').hidden = !matches.some(match => match.venueDateUnavailable);
    root.querySelector('[data-trend-undated]').hidden = !matches.some(match => match.undated);
    const measures = pointsMetric ? [3] : difference ? [2] : [0, 1];
    const visible = (effectiveSeries === 'both' ? [false, true] : [['xg', 'xpoints'].includes(effectiveSeries)]).flatMap(expected => measures.map(measure => {
      const official = T(expected ? ['xG scored', 'xG allowed', 'xG margin', 'xPoints'][measure] : ['Goals scored', 'Goals allowed', 'Goal margin', 'Points'][measure]);
      const references = relative ? trendReferences(selectedSeason, matches, measure, expected, averageReference) : [];
      const rescaled = adjusted;
      return {expected, measure, label: rescaled ? adjustedLabel(official) : official, references,
        data: shown.map((_, index) => seasonTrendValue(shown, index, measure, window, expected, rolling)),
        ...(rescaled ? {raw: matches.map((_, index) => seasonTrendValue(matches, index, measure, window, expected, rolling))} : {}),
      };
    }));
    if (pointsGap) visible.push({label: 'Points − xPoints', expected: true, gap: true, sortKey: 'points-gap', data: matches.map(match => match.values[3].expected == null ? null : match.values[3].actual - match.values[3].expected)});
    if (view === 'compare' || relative) visible.sort((a, b) => a.measure - b.measure);
    if (goalGaps) [0, 1].forEach(side => visible.push({label: T(side ? 'Goals allowed − xG allowed' : 'Goals − xG'), expected: true, gap: true, measure: side, sortKey: side ? 'allowed-gap' : 'scored-gap', data: matches.map(row => globalThis.NWSLGeometry.goalGap(marginsData.goals(row), marginsData.xg(row), side))}));
    const referenceStatus = root.querySelector('[data-trend-reference-status]');
    const missingReference = relative ? visible.filter(dataset => dataset.references[1]?.value == null && dataset.references.length > 1) : [];
    referenceStatus.hidden = !missingReference.length;
    referenceStatus.textContent = missingReference.length ? `${missingReference[0].references[1].label} unavailable for ${missingReference.map(dataset => dataset.label).join(', ')}. ${missingReference[0].references[1].note}` : '';
    showSeasonTrendTable(params, matches, visible.filter(dataset => !dataset.gap), {view, rolling, window, adjustedRows: shown});
    showPointsTiles(root.querySelector('[data-trend-points-tiles]'), shown.map(match => ({venue: match.venue, points: match.values[3].actual, xpoints: match.values[3].expected})), 'match', pointsGap, adjusted);
    root.querySelector('[data-trend-margins-panel]').hidden = !(margins || goalGaps || balanceBars || pointsGap);
    showGoalGapTiles(root.querySelector('[data-trend-goal-gap-tiles]'), matches, nonPenalty, 'match', goalGaps);
    const officialNote = root.querySelector('[data-trend-margin-official]');
    // Shown with the switch, on or off, so toggling never adds a line.
    officialNote.hidden = !(margins || balanceBars) || !(nonPenalty || opponentsShown);
    officialNote.textContent = `W–D–L and points use official results${nonPenalty ? ', including penalties' : ''}.`;
    showMarginTiles(root.querySelector('[data-trend-margin-tiles]'), shown, nonPenalty, margins || balanceBars, 'match', adjusted);
    const logos = new Map(selectedSeason.teams.map(row => [row.id, row.logo]));
    const opponentLogos = matches.map(match => logos.get(match.opponentId) || '');
    if (margins || goalGaps || balanceBars) {
      root.querySelector('[data-season-trend-plot]').hidden = false;
      for (const panel of ['goals', 'xg', 'points']) root.querySelector(`[data-trend-${panel}-panel]`).hidden = true;
      root.querySelector('[data-trend-legend]').hidden = true;
      const panelTitle = title => adjusted ? adjustedLabel(title) : title;
      root.querySelector('[data-trend-margin-title]').textContent = panelTitle(balanceBars ? T('Goals') : goalGaps ? T('Scored · Goals − xG') : nonPenalty ? 'Non-penalty goal margin' : 'Goal margin');
      root.querySelector('[data-trend-second-title]').textContent = panelTitle(balanceBars ? T('xG') : goalGaps ? T('Allowed · Goals allowed − xG allowed') : T('xG margin'));
      root.querySelector('[data-trend-second-direction]').textContent = balanceBars ? T('Up: xG for · Down: xG allowed') : goalGaps ? T('Above zero: more goals conceded than xG allowed.') : T('Up: more xG for · Zero: level · Down: more xG against');
      // Only official goal margins follow the result.
      root.querySelector('[data-trend-margin-direction]').textContent = balanceBars ? 'Up: scored · Down: allowed' : goalGaps ? T('Above zero: more goals scored than xG.') : nonPenalty || adjusted ? 'Zero: level' : 'Up: win · Zero: draw · Down: loss';
      showMatchBarPanels({panels: [{canvas: 'season-margin', key: 'season-margin'}, {canvas: 'season-margin-xg', key: 'season-margin-xg'}], matches: shown, description: `${team.name}, ${season}`, nonPenalty, matchNumbers: true, goalGaps, mirrored: balanceBars, adjusted, opponentLogos, inspection: balanceBars ? seasonBalanceInspection : goalGaps ? seasonGoalGapInspection : seasonMarginInspection});
      return;
    }
    root.querySelector('[data-trend-points-panel]').hidden = !pointsGap;
    if (pointsGap) {
      root.querySelector('[data-season-trend-plot]').hidden = false;
      root.querySelector('[data-trend-goals-panel]').hidden = true;
      root.querySelector('[data-trend-xg-panel]').hidden = true;
      root.querySelector('[data-trend-legend]').hidden = true;
      // Points and xPoints come first, drawn the same way with or without the
      // adjustment so the switch moves their bars; points − xPoints follows,
      // which the adjustment cannot move.
      root.querySelector('[data-trend-margin-title]').textContent = adjusted ? 'Adjusted points' : 'Points';
      root.querySelector('[data-trend-second-title]').textContent = adjusted ? 'Adjusted xPoints' : 'xPoints';
      root.querySelector('[data-trend-margin-direction]').textContent = 'Up: more points';
      root.querySelector('[data-trend-second-direction]').textContent = 'Up: more xPoints';
      showMatchBarPanels({panels: [{canvas: 'season-margin', key: 'season-margin'}, {canvas: 'season-margin-xg', key: 'season-margin-xg'}], matches: shown, description: `${team.name}, ${season}`, nonPenalty: false, matchNumbers: true, pointsBars: true, adjusted, opponentLogos, inspection: seasonPointsInspection});
      const hasMarks = visible.at(-1).data.some(value => value != null);
      root.querySelector('[data-trend-points-empty]').hidden = hasMarks;
      root.querySelector('[data-trend-points-wrap]').hidden = !hasMarks;
      root.querySelector('[data-trend-points-hint]').hidden = !hasMarks;
      if (hasMarks) showSeasonPointsChart(matches, team, season, opponentLogos);
      return;
    }
    const legend = relative ? [] : balance ? [
      {className: 'explore-season-scored-key', label: '━ ● Scored'},
      {className: 'explore-season-allowed-key', label: '━ □ Allowed'},
    ] : [
      // Adjusted lines name what they show; "Actual" would claim official values.
      {className: 'explore-actual-key', label: adjusted ? `━ ● Adjusted ${pointsMetric ? 'points' : nonPenalty ? 'non-penalty goals' : 'goals'}` : '━ ● Actual'},
      {className: 'explore-expected-key', label: `━ ◇ ${adjusted ? 'Adjusted ' : ''}${pointsMetric ? 'xPoints' : T('xG')}`},
    ];
    root.querySelector('[data-trend-legend]').replaceChildren(...legend.map(entry => {
      const key = document.createElement('span'); key.className = entry.className; key.textContent = entry.label; return key;
    }));
    root.querySelector('[data-trend-legend]').hidden = legend.length === 0;
    root.querySelector('[data-season-trend-plot]').hidden = false;
    const values = visible.flatMap(dataset => relative ? [...dataset.data, ...dataset.references.map(reference => reference.value)] : dataset.data).filter(value => value != null);
    const axis = trendAxis(values, signedValues);
    // Reuse two chart slots: basis panels for balance, metric panels for
    // actual/xG comparison, and one combined panel for differential.
    const panels = balance ? [
      {prefix: 'goals', label: T('Goals'), datasets: visible.filter(dataset => !dataset.expected)},
      {prefix: 'xg', label: T('Expected goals (xG)'), datasets: visible.filter(dataset => dataset.expected)},
    ] : difference || pointsMetric ? [
      {prefix: 'goals', label: pointsMetric ? 'Points vs xPoints' : 'Margins', datasets: visible},
      {prefix: 'xg', label: '', datasets: []},
    ] : [
      {prefix: 'goals', label: 'Scored', datasets: visible.filter(dataset => dataset.measure === 0)},
      {prefix: 'xg', label: 'Allowed', datasets: visible.filter(dataset => dataset.measure === 1)},
    ];
    for (const {prefix, label: panelLabel, datasets} of panels) {
      root.querySelector(`[data-trend-${prefix}-panel]`).hidden = datasets.length === 0;
      if (!datasets.length) continue;
      root.querySelector(`[data-trend-${prefix}-title]`).textContent = `${adjusted ? adjustedLabel(panelLabel) : panelLabel}${rolling ? ` · ${window}-match average` : ''}`;
      const hasMarks = datasets.some(dataset => dataset.data.some(value => value != null));
      root.querySelector(`[data-trend-${prefix}-relative-note]`).hidden = !relative;
      root.querySelector(`[data-trend-${prefix}-relative-note]`).textContent = pointsMetric ? 'Higher points per match means more points earned.' : `Above a reference: more ${prefix === 'goals' ? 'scored' : 'conceded'}.`;
      root.querySelector(`[data-trend-${prefix}-wrap]`).hidden = !hasMarks;
      const empty = root.querySelector(`[data-trend-${prefix}-empty]`); empty.hidden = hasMarks;
      empty.textContent = `No ${pointsMetric ? 'xPoints' : T('xG')} is available for these matches or fully covered rolling windows.`;
      if (!hasMarks) continue;
      const canvas = root.querySelector(`[data-chart="${prefix === 'xg' ? 'season-trend-xg' : 'season-trend'}"]`);
      const key = prefix === 'xg' ? 'seasonTrendXG' : 'seasonTrend';
      const chart = charts[key] || (charts[key] = createSeasonTrend(canvas));
      chart.matchRows = shown; chart.trendRolling = rolling; chart.trendSigned = signedValues; chart.trendAverage = relative; chart.trendReference = averageReference; chart.trendWindow = window;
      chart.data.labels = matches.map((_, index) => String(index + 1));
      chart.data.datasets = datasets.map(dataset => {
        const secondary = balance ? dataset.measure === 1 : dataset.expected;
        const ink = secondary ? balance ? color('--scoring-allowed') : xgColor : goalsColor;
        return {...dataset, type: 'line', borderColor: ink, backgroundColor: balance && secondary ? color('--paper') : ink,
          borderWidth: 2, borderDash: [], borderCapStyle: 'round',
          segment: {borderDash: context => rolling && context.p0DataIndex < window - 1 ? [1, 5] : []},
          pointStyle: secondary ? balance ? 'rect' : 'rectRot' : 'circle', pointRadius: secondary ? 4 : 3,
          pointBorderColor: ink, pointBorderWidth: 2, pointHoverRadius: 5, clip: 6, spanGaps: false, tension: 0, fill: false, order: secondary ? 1 : 0};
      });
      chart.trendPlotReferences = chart.data.datasets.flatMap(dataset => dataset.references.map((reference, index) => ({...reference, ink: index === 0 ? color('--muted') : dataset.borderColor})));
      chart.data.datasets.forEach((_, index) => chart.setDatasetVisibility(index, true));
      chart.options.layout = {padding: relative ? {top: 12, right: referenceLabelGutter(chart)} : 0};
      // trendAxis picks the ticks: Chart.js would thin them on short panels.
      chart.options.scales.y = {min: axis.min, max: axis.max, border: {display: false},
        afterBuildTicks: scale => { scale.ticks = axis.ticks.map(value => ({value})); },
        grid: {display: !relative, color: context => context.tick.value === 0 ? color('--muted') : color('--line')},
        ticks: {callback: value => `${signedValues && value > 0 ? '+' : ''}${value.toFixed(axis.decimals)}`},
        title: {display: true, text: pointsMetric ? 'Points per match' : T(relative ? effectiveSeries === 'xg' ? 'xG per match' : effectiveSeries === 'goals' ? 'Goals per match' : 'Goals / xG per match' : balance ? `${prefix === 'xg' ? 'xG' : 'Goals'} per match` : difference ? 'Margin per match' : 'Goals / xG per match')}};
      const dataDescription = pointsMetric ? relative ? effectiveSeries === 'points' ? 'actual points' : 'xPoints' : 'points and xPoints' : balance ? 'scored and allowed' : relative ? effectiveSeries === 'goals' ? `actual ${goalsWord}` : T('xG') : `actual ${goalsWord} and ${T('xG')}`;
      canvas.setAttribute('aria-label', `${team.name}, ${season}: ${panelLabel}, ${dataDescription}, ${rolling ? `${window}-match rolling averages using all matches played so far before the first full window` : 'per-match values'}${relative ? `, original values with team and selected benchmark references` : ''}`);
      chart.resize(); chart.update('none');
    }
  }
  function showTeamRankings(params) {
    const season = params.get('season') || root.dataset.defaultTeamSeason;
    const teamID = params.get('team') || root.dataset.defaultHistoryTeam;
    const units = params.get('units') === 'total' ? 'total' : 'per-match';
    Object.entries({season, team: teamID, units}).forEach(([key, value]) => {
      rankingControls.elements.namedItem(key).value = value;
    });
    const selectedSeason = teamSeasons.find(row => row.season === season);
    const team = selectedSeason?.teams?.find(row => row.id === teamID);
    root.querySelector('[data-rankings-empty]').hidden = !!team;
    root.querySelector('[data-rankings-results]').hidden = !team;
    if (!team) return;
    const count = selectedSeason.teams.length;
    root.querySelector('[data-rankings-team]').replaceChildren(teamName(team));
    root.querySelector('[data-rankings-summary]').textContent = `${season} · ${team.played} played · ${count} teams${selectedSeason.active ? ' · In progress' : ''}`;
    root.querySelector('[data-rankings-units]').textContent = `${units === 'total' ? 'Regular-season totals' : 'Per regular-season match'}, from recorded results. ${T('Lower goals allowed and xG allowed rank better; higher is better for the other stats.')}${nonPenalty ? ' Penalty kicks excluded.' : ''}`;
    root.querySelector('[data-rankings-warning]').hidden = !selectedSeason.teams.some(row => row.values[0].expected === null);
    root.querySelector('[data-rankings-cards]').replaceChildren(...team.rankings[units === 'total' ? 1 : 0].map(row => {
      const card = document.createElement('article'); card.className = 'explore-rank-card';
      const title = document.createElement('h4'); title.textContent = row.label;
      const rank = document.createElement('p'); rank.className = 'explore-rank-number'; rank.textContent = row.rank;
      if (row.position) {
        const denominator = document.createElement('span'); denominator.textContent = ` of ${count}`;
        rank.append(denominator);
      }
      const value = document.createElement('p'); value.className = 'explore-rank-value';
      value.textContent = row.value + (row.value === 'Unavailable' ? '' : units === 'total' ? ' total' : ' per match');
      card.append(title, rank, value);
      if (row.position) {
        const track = document.createElement('div'); track.className = 'explore-rank-track'; track.setAttribute('aria-hidden', 'true');
        const mark = document.createElement('i'); mark.style.left = `${row.position}%`; track.append(mark);
        const endpoints = document.createElement('div'); endpoints.className = 'explore-rank-endpoints'; endpoints.setAttribute('aria-hidden', 'true');
        const best = document.createElement('span'); best.textContent = '1st · Best';
        const last = document.createElement('span'); last.textContent = `${count} · Last`;
        endpoints.append(best, last); card.append(track, endpoints);
      }
      return card;
    }));
  }
  const archivePayload = JSON.parse(root.querySelector('#explore-team-seasons-data').textContent);
  const archiveControls = root.querySelector('[data-team-seasons-controls]');
  // The chart card's controls submit with the form above them; its segmented
  // controls are RadioNodeLists.
  const archivePanel = root.querySelector('[data-panel="team-seasons"]');
  const archiveDisplay = archiveControls.elements.namedItem('team-seasons-display');
  const archiveMeasure = archiveControls.elements.namedItem('team-seasons-measure');
  const archiveRankBy = archiveControls.elements.namedItem('team-seasons-rank-by');
  const archivePenalties = root.querySelector('[data-team-seasons-penalties]');
  const archiveMeasures = ['for', 'against', 'difference', 'points'];
  const archiveRankBys = ['actual', 'expected', 'gap'];
  const archiveLabels = ['Goals scored', 'Goals allowed', 'Goal differential', 'Points'];
  const archiveText = (text, np) => np ? text.replace(basisPattern, basisReplacement) : text;
  const archiveTie = (a, b) => a.season !== b.season ? (a.season > b.season ? -1 : 1) : a.name !== b.name ? (a.name < b.name ? -1 : 1) : a.id < b.id ? -1 : a.id === b.id ? 0 : 1;
  const archiveMissing = (measure, np) => measure === 3 ? 'xPoints incomplete' : archiveText('xG incomplete', np);
  const archiveLink = (params, changes) => {
    const target = new URLSearchParams(params);
    Object.entries(changes).forEach(([key, value]) => target.set(key, value));
    const measure = target.get('team-seasons-measure') || 'points';
    if (measure === 'points' || target.get('penalties') === 'include') target.delete('penalties');
    if (target.get('team-seasons-display') === 'ranking') target.delete('team-seasons-display');
    return `?${target}`;
  };
  function archiveElement(tag, text, className) {
    const element = document.createElement(tag);
    if (text !== undefined) element.textContent = text;
    if (className) element.className = className;
    return element;
  }
  function clearArchiveIdentification(chart, redraw = true) {
    if (!chart) return;
    chart.archiveIdentified = [];
    root.querySelector('[data-team-seasons-identification]').hidden = true;
    root.querySelector('[data-team-seasons-identification]').replaceChildren();
    chart.canvas.setAttribute('aria-label', chart.archiveDescription);
    if (redraw) chart.update('none');
  }
  function archiveDescription(chart, index) {
    const row = chart.teamRows[index], v = row.values[chart.archiveMeasure];
    return `${row.name} · ${row.season}. Actual: ${fixed(v.actual)}. Expected: ${fixed(v.expected)}. Gap: ${signed(v.gap)} per match.${row.active ? ` In progress through ${row.played} matches.` : ''}`;
  }
  function identifyArchivePoint(chart, index) {
    if (index === undefined) { clearArchiveIdentification(chart); return; }
    const indexes = window.NWSLGeometry.scatterGroup(scatterMarks(chart), index);
    chart.archiveIdentified = indexes;
    const label = root.querySelector('[data-team-seasons-identification]');
    label.replaceChildren();
    if (indexes.length > 1) label.append(archiveElement('p', `${indexes.length} team-seasons at this point`));
    indexes.forEach(i => {
      const row = chart.teamRows[i], v = row.values[chart.archiveMeasure];
      const line = archiveElement('p', undefined, 'explore-archive-identification');
      line.append(teamName(row), document.createTextNode(` · ${row.season} · Actual ${fixed(v.actual)} · Expected ${fixed(v.expected)} · Gap ${signed(v.gap)}${row.active ? ` · in progress through ${row.played} matches` : ''}`));
      label.append(line);
    });
    label.hidden = !indexes.length;
    chart.canvas.setAttribute('aria-label', `${chart.archiveDescription} ${indexes.map(i => archiveDescription(chart, i)).join(' ')}`);
    chart.update('none');
  }
  function createArchiveScatter(canvas) {
    const options = commonOptions();
    options.layout = scatterLayout();
    options.interaction = {mode: 'point', intersect: true};
    options.onClick = (event, elements, chart) => {
      const nearest = elements.map(mark => ({index: mark.index, point: chart.getDatasetMeta(0).data[mark.index]}))
        .sort((a, b) => Math.hypot(a.point.x - event.x, a.point.y - event.y) - Math.hypot(b.point.x - event.x, b.point.y - event.y))[0];
      identifyArchivePoint(chart, nearest?.index);
    };
    options.onHover = (_, elements, chart) => { chart.canvas.style.cursor = elements.length ? 'pointer' : ''; };
    options.plugins.tooltip.callbacks = {title: () => '', label: item => {
      const row = item.chart.teamRows[item.dataIndex], v = row.values[item.chart.archiveMeasure];
      return [`${row.name} · ${row.season}`, `Actual: ${fixed(v.actual)} per match`, `Expected: ${fixed(v.expected)} per match`, `Gap: ${signed(v.gap)} per match`, ...(row.active ? [`In progress through ${row.played} matches`] : [])];
    }};
    const axis = text => ({type: 'linear', grid: {color: color('--line')}, border: {display: false}, ticks: {maxTicksLimit: 6}, title: {display: true, text}});
    options.scales = {x: axis('Expected per match'), y: axis('Actual per match')};
    const chart = new Chart(canvas, {type: 'scatter', options, plugins: [{id: 'archiveScatterParity',
      afterLayout: chart => { chart.scatterPointRadius = pointRadius(scatterMarks(chart), chart.chartArea, 3, 6); },
      beforeDatasetsDraw(chart) {
        const {x, y} = chart.scales, ctx = chart.ctx;
        ctx.save(); ctx.strokeStyle = color('--muted'); ctx.lineWidth = 1.5; ctx.setLineDash([6, 5]);
        ctx.beginPath(); ctx.moveTo(x.getPixelForValue(x.min), y.getPixelForValue(x.min));
        ctx.lineTo(x.getPixelForValue(x.max), y.getPixelForValue(x.max)); ctx.stroke(); ctx.restore();
      },
      afterDatasetsDraw(chart) {
        const ctx = chart.ctx; ctx.save(); ctx.strokeStyle = color('--gold'); ctx.lineWidth = 2;
        (chart.archiveIdentified || []).forEach(index => {
          const point = chart.getDatasetMeta(0).data[index];
          ctx.beginPath(); ctx.arc(point.x, point.y, (chart.scatterPointRadius || 6) + 3, 0, Math.PI * 2); ctx.stroke();
        }); ctx.restore();
      },
    }], data: {labels: [], datasets: [{label: 'Team-seasons', data: [],
      pointRadius: context => context.chart.scatterPointRadius || 6,
      pointHoverRadius: context => (context.chart.scatterPointRadius || 6) + 2,
      pointHitRadius: 5, borderWidth: 2, borderColor: goalsColor,
      backgroundColor: context => context.chart.teamRows?.[context.dataIndex]?.active ? color('--panel') : goalsColor,
    }]}});
    keyboardAccess(chart, mark => archiveDescription(chart, mark.index));
    canvas.addEventListener('keydown', event => {
      if (!['Enter', ' '].includes(event.key)) return;
      event.preventDefault(); identifyArchivePoint(chart, chart.getActiveElements()[0]?.index);
    });
    return chart;
  }
  function showArchiveScatter(rows, measure, actualLabel, expectedLabel) {
    const placeable = rows.filter(row => row.values[measure].actual !== null && row.values[measure].expected !== null)
      .sort((a, b) => a.values[measure].expected - b.values[measure].expected || archiveTie(a, b));
    const empty = !placeable.length;
    root.querySelector('[data-team-seasons-scatter-wrap]').hidden = empty;
    root.querySelector('[data-team-seasons-scatter-empty]').hidden = !empty;
    const description = `Eligible regular-season team-seasons since 2016. One dot per team-season with complete expected data. Horizontal axis: ${expectedLabel} per match (expected). Vertical axis: ${actualLabel} per match (actual). Dashed diagonal: actual matches expected. Hollow dots are in-progress seasons.`;
    root.querySelector('[data-team-seasons-scatter-description]').textContent = description;
    const meaning = `Above: ${measure === 2 ? 'higher' : 'more'} ${actualLabel.toLowerCase()} than ${expectedLabel}; below: ${measure === 2 ? 'lower' : 'fewer'}.`;
    root.querySelector('[data-team-seasons-scatter-meaning]').textContent = `One dot per team-season; hollow dots are in progress. Dashed diagonal: actual matches expected. ${meaning}`;
    if (empty) return;
    const canvas = root.querySelector('[data-chart="team-seasons-scatter"]');
    const chart = charts.archiveScatter || (charts.archiveScatter = createArchiveScatter(canvas));
    chart.teamRows = placeable; chart.archiveMeasure = measure; chart.archiveDescription = description;
    clearArchiveIdentification(chart, false);
    const domain = measure === 3 ? {min: 0, max: 3} : scatterDomain(placeable, {index: measure}, 'per-match', true);
    chart.data.labels = placeable.map(row => `${row.name} · ${row.season}`);
    chart.data.datasets[0].data = placeable.map(row => ({x: row.values[measure].expected, y: row.values[measure].actual}));
    chart.options.scales.x.min = chart.options.scales.y.min = domain.min;
    chart.options.scales.x.max = chart.options.scales.y.max = domain.max;
    chart.options.scales.x.title.text = `${expectedLabel} per match (expected)`;
    chart.options.scales.y.title.text = `${actualLabel} per match (actual)`;
    chart.resize(); chart.update('none');
  }
  function showTeamSeasons(params) {
    const measureKey = params.get('team-seasons-measure') || 'points', measure = archiveMeasures.indexOf(measureKey);
    const scatter = params.get('team-seasons-display') === 'scatter';
    const byKey = params.get('team-seasons-rank-by') || 'actual', by = scatter ? 0 : archiveRankBys.indexOf(byKey);
    archivePanel.querySelector('[data-segmented="team-seasons-rank-by"]').hidden = scatter;
    root.querySelector('[data-team-seasons-chart]').hidden = scatter;
    root.querySelector('[data-team-seasons-scatter]').hidden = !scatter;
    archiveDisplay.value = scatter ? 'scatter' : 'ranking';
    const np = measureKey !== 'points' && params.get('penalties') === 'exclude';
    const records = np ? archivePayload.nonPenalty : archivePayload.all;
    const rows = records.filter(row => row.values[measure].eligible);
    const rank = row => row.values[measure].ranks[by];
    const ranked = rows.filter(row => rank(row)).sort((a, b) => rank(a) - rank(b) || archiveTie(a, b));
    const missing = rows.filter(row => scatter ? row.values[measure].expected === null : !rank(row));
    const missingLabel = archiveMissing(measure, np);
    archiveMeasure.value = measureKey; archiveRankBy.value = byKey;
    archivePenalties.disabled = measureKey === 'points'; archivePenalties.checked = np;
    root.querySelector('[data-team-seasons-penalties-control]').hidden = measureKey === 'points';
    root.querySelector('[data-team-seasons-points-note]').hidden = measureKey !== 'points' || params.get('penalties') !== 'exclude';
    const actualLabel = archiveText(archiveLabels[measure], np), expectedLabel = archiveText(['xG', 'xG allowed', 'xGD', 'xPoints'][measure], np);
    const companionLabel = by === 1 ? actualLabel : expectedLabel;
    const headingMeasure = by === 1 ? `Expected ${actualLabel.toLowerCase()}` : by === 2 ? `${actualLabel} compared with expected,` : actualLabel;
    const heading = scatter ? `${actualLabel} vs expected per match, since 2016` : `${headingMeasure} per match, since 2016`;
    root.querySelector('[data-team-seasons-title]').textContent = heading;
    root.querySelector('[data-team-seasons-chart]').setAttribute('aria-label', `${heading}, ranked ${byKey} values with comparisons`);
    const legend = root.querySelector('[data-team-seasons-legend]');
    legend.textContent = by === 2 ? 'Bar: actual − expected' : `Bar: ${byKey} · `;
    if (by !== 2) {
      const swatch = archiveElement('span', undefined, 'explore-archive-marker-swatch'); swatch.setAttribute('aria-hidden', 'true');
      legend.append(swatch, document.createTextNode(` ${companionLabel}`));
    }
    const note = root.querySelector('[data-team-seasons-missing]');
    note.hidden = !missing.length;
    note.textContent = missing.length ? `${missing.length} team-seasons have ${missingLabel} ${scatter ? 'and cannot be plotted. Their actual values remain in the table.' : 'and are not ranked. Their actual values appear last in the table.'}` : '';
    root.querySelector('[data-team-seasons-standing-note]').textContent = `Regular-season rates adjust for matches played, not opponents or era; this is not a judgment of the best team. Active seasons enter once every team has 10 valid matches; hollow ${scatter ? 'dots' : 'bars'} are in progress.`;
    const gapHelp = root.querySelector('[data-team-seasons-gap-help]');
    gapHelp.hidden = scatter || by !== 2;
    gapHelp.textContent = `Gap is actual − expected. Positive means above expected; negative means below expected.${measure === 1 ? ' Fewer goals allowed than expected rank first.' : ''}`;
    let high = ['Most goals scored per match', 'Fewest goals allowed per match', 'Highest goal differential per match', 'Highest points per match'][measure];
    let low = ['Fewest goals scored per match', 'Most goals allowed per match', 'Lowest goal differential per match', 'Lowest points per match'][measure];
    if (by === 1) { high = `Expected: ${high}`; low = `Expected: ${low}`; }
    if (by === 2) {
      high = measure === 1 ? 'Fewest goals allowed relative to expected' : 'Most above expected';
      low = measure === 1 ? 'Most goals allowed relative to expected' : 'Most below expected';
    }
    const endText = text => archiveText(np ? text.replaceAll('goals', 'non-penalty goals') : text, np);
    root.querySelector('[data-team-seasons-high]').textContent = endText(high);
    const numbers = ranked.flatMap(row => by === 2 ? [row.values[measure].gap] : [row.values[measure].actual, row.values[measure].expected]).filter(value => value !== null);
    const domain = window.NWSLGeometry.rankingDomain(numbers, by === 2 || measure === 2, measure === 3 && by !== 2);
    const axis = root.querySelector('[data-team-seasons-axis]');
    axis.replaceChildren(archiveElement('span', domain.min), archiveElement('span', `Per match · ${domain.min === 0 ? 'starts at 0' : '0 at center'}`), archiveElement('span', domain.max));
    root.querySelector('[data-team-seasons-low]').textContent = endText(low);
    const visible = ranked.length > 20 ? [...ranked.slice(0, 10), ...ranked.slice(-10)] : ranked;
    const bars = visible.map(row => {
      const v = row.values[measure], value = v[archiveRankBys[by]], companion = by === 2 ? null : by === 1 ? v.actual : v.expected;
      const geometry = window.NWSLGeometry.rankingBar(value, companion, domain);
      const item = archiveElement('li', undefined, `explore-archive-bar${value < 0 ? ' is-negative' : ''}${row.active ? ' is-active' : ''}${value === 0 ? ' is-zero' : ''}`);
      item.dataset.teamSeasonsRow = ''; item.dataset.teamId = row.id; item.dataset.season = row.season;
      const label = archiveElement('span', undefined, 'explore-archive-label');
      const identity = archiveElement('span', undefined, 'explore-archive-identity');
      identity.append(teamName(row), archiveElement('span', `· ${row.season}`));
      label.append(archiveElement('span', `${rank(row)}.`, 'explore-archive-rank'), identity);
      if (row.active) label.append(archiveElement('small', `in progress through ${row.played} matches`));
      const track = archiveElement('span', undefined, 'explore-archive-track'); track.setAttribute('aria-hidden', 'true');
      const zero = archiveElement('span', undefined, 'explore-archive-zero'); zero.style.left = `${geometry.zero}%`;
      const bar = archiveElement('i'); bar.style.left = `${geometry.left}%`; bar.style.width = `${geometry.width}%`; track.append(zero, bar);
      if (companion !== null) { const marker = archiveElement('b', undefined, 'explore-archive-marker'); marker.style.left = `${geometry.marker}%`; track.append(marker); }
      const values = archiveElement('span', undefined, 'explore-archive-numbers'); values.append(archiveElement('strong', by === 2 ? signed(value) : fixed(value)));
      if (by !== 2) values.append(archiveElement('small', companion === null ? missingLabel : `${companionLabel} ${fixed(companion)}`));
      item.append(label, track, values); return item;
    });
    const split = Math.min(10, Math.ceil(bars.length / 2));
    root.querySelector('[data-team-seasons-bars="high"]').replaceChildren(...bars.slice(0, split));
    root.querySelector('[data-team-seasons-bars="low"]').replaceChildren(...bars.slice(split));
    const more = Math.max(0, ranked.length - 20), breakRow = root.querySelector('[data-team-seasons-break]');
    breakRow.hidden = !more;
    const breakButton = archiveElement('button', `${more} more team-seasons in the table`); breakButton.type = 'button';
    breakButton.addEventListener('click', () => {
      root.querySelector('[data-team-seasons-values]').open = true;
      const table = root.querySelector('[data-team-seasons-table]'); table.focus(); table.scrollIntoView({block: 'start'});
    });
    breakRow.replaceChildren(breakButton);
    root.querySelector('[data-team-seasons-chart-empty]').hidden = !!ranked.length;
    root.querySelector('[data-team-seasons-summary]').textContent = `Table: all ${rows.length} team-seasons`;
    const expanded = params.has('team-seasons-sort') || params.has('team-seasons-order');
    root.querySelector('[data-team-seasons-values]').open = expanded;
    const column = params.get('team-seasons-sort') || 'rank', order = params.get('team-seasons-order') || 'asc';
    for (const [key, value] of [['team-seasons-sort', column], ['team-seasons-order', order]]) {
      const input = archiveControls.elements.namedItem(key); input.value = value; input.disabled = !expanded;
    }
    root.querySelectorAll('[data-team-seasons-sort]').forEach(link => {
      const active = link.dataset.teamSeasonsSort === column;
      link.parentElement.setAttribute('aria-sort', active ? (order === 'asc' ? 'ascending' : 'descending') : 'none');
      link.href = archiveLink(params, {view: 'team-seasons', 'team-seasons-sort': link.dataset.teamSeasonsSort, 'team-seasons-order': active && order === 'asc' ? 'desc' : 'asc'});
    });
    const value = row => column === 'rank' ? rank(row) : column === 'team' ? row.name : ['season', 'played'].includes(column) ? row[column] : row.values[measure][column];
    rows.sort((a, b) => {
      if (!!rank(a) !== !!rank(b)) return rank(a) ? -1 : 1;
      const x = value(a), y = value(b);
      if ((x === null) !== (y === null)) return x === null ? 1 : -1;
      if (x !== null && x !== y) return (x < y ? -1 : 1) * (order === 'asc' ? 1 : -1);
      return archiveTie(a, b);
    });
    root.querySelector('[data-team-seasons-table-caption]').textContent = `${actualLabel} per match. Expected: ${expectedLabel}. Gap: actual − expected. Tied rows share a competition rank.`;
    if (scatter) showArchiveScatter(rows, measure, actualLabel, expectedLabel);
    const tableBody = root.querySelector('[data-team-seasons-rows]');
    tableBody.replaceChildren(...rows.map(row => {
      const v = row.values[measure], tr = archiveElement('tr'); tr.dataset.teamId = row.id; tr.dataset.season = row.season;
      tr.append(archiveElement('td', rank(row) || missingLabel));
      const th = archiveElement('th'); th.scope = 'row'; th.append(teamName(row)); tr.append(th);
      tr.append(archiveElement('td', `${row.season}${row.active ? ` (in progress through ${row.played} matches)` : ''}`), archiveElement('td', row.played));
      for (const key of ['actual', 'expected', 'gap']) tr.append(archiveElement('td', v[key] === null ? key === 'actual' ? 'Unavailable' : missingLabel : key === 'gap' ? signed(v[key]) : fixed(v[key])));
      return tr;
    }));

  }
  const changeArchive = event => {
    const measure = archiveMeasure.value;
    const changes = {view: 'team-seasons', 'team-seasons-measure': measure, 'team-seasons-rank-by': archiveRankBy.value};
    // A measure change resets the basis; a penalties change owns its own value.
    changes.penalties = measure === 'points' || event.target.name === 'team-seasons-measure' || !archivePenalties.checked ? null : 'exclude';
    update(changes);
  };
  archiveControls.addEventListener('submit', event => { event.preventDefault(); changeArchive(event); });
  // The chart choice keeps every other selection; Ranking is the default.
  archivePanel.addEventListener('change', event => {
    if (event.target.form !== archiveControls) return;
    if (event.target.name === 'team-seasons-display') update({view: 'team-seasons', 'team-seasons-display': archiveDisplay.value === 'ranking' ? null : archiveDisplay.value});
    else changeArchive(event);
  });

  const h2hTeam = root.querySelector('[data-h2h-team]');
  const h2hOpponent = root.querySelector('[data-h2h-opponent]');
  // A RadioNodeList: the chart picker's radios submit with the pair form.
  const h2hSeries = root.querySelector('#h2h-controls').elements.namedItem('h2h-series');
  const h2hPenalties = root.querySelector('[data-h2h-penalties]');
  // Adjusted tiles total opponent-adjusted points in the same lines, as
  // exploreAdjustedPointsTiles in Go.
  function showPointsTiles(container, rows, unit, shown, adjusted = false) {
    container.hidden = !shown || !rows.length;
    if (!shown) return;
    container.replaceChildren(...['All', 'Home', 'Away'].map(venue => {
      const matches = rows.filter(row => venue === 'All' || row.venue === venue), n = matches.length;
      const covered = matches.filter(row => row.xpoints != null), complete = n > 0 && covered.length === n;
      const actual = matches.reduce((sum, row) => sum + row.points, 0);
      const expected = covered.reduce((sum, row) => sum + row.xpoints, 0), gap = actual - expected;
      const tile = archiveElement('div'); tile.className = 'explore-h2h-tile explore-points-tile'; tile.dataset.pointsTile = venue;
      const label = archiveElement('div'); label.className = 'explore-h2h-tile-label';
      if (venue !== 'All') { const swatch = archiveElement('i'); swatch.dataset.h2hEncoding = venue.toLowerCase(); swatch.setAttribute('aria-hidden', 'true'); label.append(swatch); }
      label.append(venue);
      if (adjusted && matches.some(row => row.points == null)) { tile.append(label, archiveElement('strong', 'Points unavailable')); return tile; }
      const total = archiveElement('strong', n ? `${adjusted ? fixed(actual) : actual} ` : unit === 'match' ? 'No matches' : 'No meetings');
      if (n) total.append(archiveElement('span', `vs ${complete ? fixed(expected) + ' xPoints' : 'xPoints incomplete'}`));
      tile.append(label, total);
      if (n) tile.append(archiveElement('div', complete ? `${fixed(Math.abs(gap))} ${gap < 0 ? 'below' : 'above'} expected` : 'xPoints incomplete'));
      // Coverage is stated only when it qualifies the totals.
      if (n && !complete) { const coverage = archiveElement('div', `${covered.length} of ${n} ${unit === 'match' ? 'matches' : 'meetings'}`); coverage.className = 'note'; tile.append(coverage); }
      return tile;
    }));
  }
  function showMarginTiles(container, matches, np, shown, unit = 'match', adjusted = false) {
    container.hidden = !shown || !matches.length;
    if (!shown) return;
    const basis = {nonPenalty: np, xgLabel: archiveText('xG', np), adjusted};
    container.replaceChildren(...marginsData.summarize(matches).map(total => {
      const view = marginsData.format(total, basis, unit), venue = view.label;
      const tile = archiveElement('div'); tile.className = 'explore-h2h-tile explore-margin-tile'; tile.dataset.h2hTile = venue;
      const label = archiveElement('div'); label.className = 'explore-h2h-tile-label';
      if (venue !== 'All') { const swatch = archiveElement('i'); swatch.dataset.h2hEncoding = venue.toLowerCase(); swatch.setAttribute('aria-hidden', 'true'); label.append(swatch); }
      label.append(document.createTextNode(venue)); tile.append(label);
      const wdl = archiveElement('strong', view.record); wdl.className = 'explore-h2h-wdl';
      wdl.setAttribute('aria-label', view.recordLabel); tile.append(wdl);
      tile.append(archiveElement('div', view.rate), archiveElement('div', view.goals));
      if (view.coverage) tile.append(archiveElement('div', view.coverage));
      const xg = archiveElement('div', view.xg); xg.className = 'explore-margin-xg'; tile.append(xg);
      if (view.xgCoverage) tile.append(archiveElement('div', view.xgCoverage));
      tile.append(archiveElement('div', view.matches)); return tile;
    }));
  }
  function showGoalGapTiles(container, matches, np, unit, shown) {
    container.hidden = !shown || !matches.length;
    if (!shown) return;
    container.replaceChildren(...marginsData.summarize(matches).map(total => {
      const view = marginsData.formatGoalGap(total, {nonPenalty: np, xgLabel: archiveText('xG', np)}, unit);
      const tile = archiveElement('div'); tile.className = 'explore-h2h-tile explore-goal-gap-tile'; tile.dataset.goalGapTile = view.label;
      const label = archiveElement('div'); label.className = 'explore-h2h-tile-label';
      if (view.label !== 'All') { const swatch = archiveElement('i'); swatch.dataset.h2hEncoding = view.label.toLowerCase(); swatch.setAttribute('aria-hidden', 'true'); label.append(swatch); }
      label.append(view.label); tile.append(label);
      if (view.empty) tile.append(archiveElement('div', view.empty));
      for (const side of view.sides) {
        const block = archiveElement('div'); block.className = 'explore-goal-gap-side';
        const heading = archiveElement('span', side.heading); heading.className = 'explore-goal-gap-heading';
        block.append(heading, archiveElement('strong', side.value));
        if (side.gap) block.append(archiveElement('span', side.gap));
        tile.append(block);
      }
      for (const text of [view.coverage, view.splitCoverage].filter(Boolean)) { const line = archiveElement('div', text); line.className = 'note'; tile.append(line); }
      return tile;
    }));
  }
  // One entry per bar layout: its series per panel, and the text and tick
  // format each panel carries. `sides` is how many bar series a panel draws.
  function matchBarMode({goalGaps, mirrored, pointsBars, np, xg, adjusted = false}) {
    const {goals, xg: xgPair, goalMargin, xgMargin} = marginsData, {goalGap} = window.NWSLGeometry;
    const signed = (value, decimals) => `${value > 0 ? '+' : ''}${value.toFixed(decimals)}`;
    if (pointsBars) {
      // Opponent-adjusted points, then xPoints, on one fixed whole-point scale.
      return {
        series: ['actual', 'expected'].map(key => [matches => matches.map(row => row.values[matchMeasure.points][key] ?? null)]),
        label: i => i ? 'xPoints' : 'Points',
        seriesLabel: label => label,
        direction: i => `up: more ${i ? 'xPoints' : 'points'}`,
        tick: value => `${value}`,
        axis: window.NWSLGeometry.pointsBarAxis,
        grouped: true,
      };
    }
    if (mirrored) {
      // Preserve nulls before negating allowed counts so an uncovered match stays empty.
      const side = (read, index) => matches => matches.map(row => { const value = read(row)[index]; return value === null ? null : index ? -value : value; });
      return {
        series: [goals, xgPair].map(read => [side(read, 0), side(read, 1)]),
        label: i => archiveText(i ? 'xG' : 'Goals', np),
        seriesLabel: (label, index) => `${label} ${index ? 'allowed' : 'for'}`,
        direction: i => `Up: ${i ? xg + ' for' : 'scored'}; Down: ${i ? xg + ' allowed' : 'allowed'}`,
        tick: (value, decimals) => Math.abs(value).toFixed(decimals),
        grouped: false,
      };
    }
    const single = goalGaps
      ? [0, 1].map(index => matches => matches.map(row => goalGap(goals(row), xgPair(row), index)))
      : [matches => matches.map(goalMargin), matches => matches.map(xgMargin)];
    return {
      series: single.map(read => [read]),
      label: i => goalGaps ? archiveText(i ? 'Goals against − xG against' : 'Goals for − xG for', np) : i ? `${xg} margin` : np ? 'Non-penalty goal margin' : 'Goal margin',
      seriesLabel: label => label,
      direction: i => goalGaps ? archiveText(i ? 'Above zero: more goals conceded than xG allowed.' : 'Above zero: more goals scored than xG.', np) : i ? `up: more ${xg} for; zero: level; down: more ${xg} against` : np || adjusted ? 'zero: level' : 'up: win; zero: draw; down: loss',
      tick: signed,
      grouped: true,
    };
  }
  function showMatchBarPanels({panels, matches, description, nonPenalty: np, matchNumbers, inspection = headToHeadInspection, goalGaps = false, mirrored = false, pointsBars = false, adjusted = false, opponentLogos = null}) {
    const xg = archiveText('xG', np), mode = matchBarMode({goalGaps, mirrored, pointsBars, np, xg, adjusted});
    if (adjusted) { const label = mode.label; mode.label = i => adjustedLabel(label(i)); }
    const values = mode.series.map(panel => panel.map(read => read(matches)));
    const axis = (mode.axis || window.NWSLGeometry.marginAxis)(...values.map(panel => panel.flat()));
    panels.forEach(({canvas: name, key}, i) => {
      const canvas = root.querySelector(`[data-chart="${name}"]`);
      const chart = charts[key] || (charts[key] = createHeadToHead(canvas, matchNumbers, inspection));
      chart.inspection = inspection;
      chart.meetings = matches;
      chart.data.labels = matches.map((_, index) => String(index));
      const label = mode.label(i);
      chart.data.datasets = values[i].map((data, index) => ({data, label: mode.seriesLabel(label, index), ...venueBarStyle(canvas), grouped: mode.grouped}));
      Object.assign(chart.options.scales.y, {min: axis.min, max: axis.max,
        afterBuildTicks: scale => { scale.ticks = axis.ticks.map(value => ({value})); },
        afterFit: scale => { scale.width = 40; },
        ticks: {callback: value => mode.tick(value, axis.decimals)}});
      canvas.setAttribute('aria-label', `${description}: ${label}, each ${matchNumbers ? 'match' : 'meeting'}; ${mode.direction(i)}; home solid blue, away striped orange`);
      chart.opponentLogos = opponentLogos;
      syncOpponentLogoRow(chart);
      chart.resize(); chart.update('none');
    });
  }
  // Points − xPoints always spans the fixed −3 to +3 range; label every whole
  // point, signed like the other gap axes.
  function pointsGapAxis(chart) {
    const {extent} = window.NWSLGeometry.headToHeadGeometry([], chart.width, chart.height, 'points');
    const ticks = Array.from({length: 2 * extent + 1}, (_, index) => index - extent);
    Object.assign(chart.options.scales.y, {min: -extent, max: extent,
      afterBuildTicks: scale => { scale.ticks = ticks.map(value => ({value})); },
      ticks: {callback: value => `${value > 0 ? '+' : ''}${value}`}});
  }
  function showSeasonPointsChart(matches, team, season, opponentLogos) {
    const canvas = root.querySelector('[data-chart="season-points-gap"]');
    const chart = charts.seasonPointsGap || (charts.seasonPointsGap = createHeadToHead(canvas, true));
    chart.meetings = matches.map((match, index) => ({...match, date: `Match ${index + 1} · ${match.date}`, pointsView: true, points: match.values[3].actual, xpoints: match.values[3].expected, result: {3: 'Win', 1: 'Draw', 0: 'Loss'}[match.values[3].actual]}));
    chart.data.labels = matches.map((_, index) => String(index + 1));
    chart.data.datasets[0].data = chart.meetings.map(row => window.NWSLGeometry.pointsGap(row.points, row.xpoints));
    chart.data.datasets[0].label = 'Points − xPoints';
    pointsGapAxis(chart);
    canvas.setAttribute('aria-label', `${team.name}, ${season}: points minus xPoints, each match; above zero: more points than xPoints; below zero: fewer; home solid blue, away striped orange`);
    chart.opponentLogos = opponentLogos;
    syncOpponentLogoRow(chart);
    chart.resize(); chart.update('none');
  }
  function showHeadToHead(params) {
    if (!h2hData.teams?.length) return;
    const series = ['both', 'points'].includes(params.get('h2h-series')) ? params.get('h2h-series') : 'actual';
    const pointsView = series === 'points', both = series === 'both', margins = series === 'actual', np = !pointsView && params.get('penalties') === 'exclude';
    const label = text => np ? text.replace(basisPattern, basisReplacement) : text;
    const xgLabel = label('xG');
    h2hSeries.value = series;
    root.querySelector('[data-h2h-penalties-label]').hidden = pointsView;
    h2hPenalties.disabled = pointsView;
    root.querySelector('[data-h2h-points-title]').hidden = !pointsView;
    h2hPenalties.checked = params.get('penalties') === 'exclude';
    const carriedSeries = root.querySelector('[data-h2h-carried-series]'); carriedSeries.disabled = margins; carriedSeries.value = h2hSeries.value;
    root.querySelector('[data-h2h-goal-title]').hidden = pointsView;
    root.querySelector('[data-h2h-goal-title]').textContent = both ? label('Goals for − xG for') : np ? 'Non-penalty goal margin' : 'Goal margin';
    root.querySelector('[data-h2h-xg-panel]').hidden = pointsView;
    root.querySelector('[data-h2h-xg-panel] h3').textContent = both ? label('Goals against − xG against') : `${xgLabel} margin`;
    root.querySelector('[data-h2h-xg-panel] p').textContent = both ? label('Above zero: more goals conceded than xG allowed.') : `Up: more ${xgLabel} for · Zero: level · Down: more ${xgLabel} against`;
    root.querySelector('[data-h2h-direction]').textContent = pointsView ? 'Above zero: more points than xPoints.' : both ? label('Above zero: more goals scored than xG.') : np ? 'Zero: level' : 'Up: win · Zero: draw · Down: loss';
    root.querySelector('th[data-h2h-basis-column]').hidden = !np;
    const headers = root.querySelectorAll('th[data-h2h-expected-column]');
    [xgLabel + ' for', xgLabel + ' against', 'xPoints', pointsView ? 'Points − xPoints' : both ? label('Goals for − xG for') : xgLabel + ' margin', label('Goals against − xG against')].forEach((text, i) => { headers[i].textContent = text; headers[i].hidden = i === 4 ? !both : pointsView ? i < 2 : i === 2; });
    const teamIndex = h2hData.teams.findIndex(t => t.ID === (params.get('team') || root.dataset.defaultH2hTeam));
    if (teamIndex < 0) return;
    const data = {team: h2hData.teams[teamIndex]};
    const identity = t => ({id: t.ID, name: t.Name, logo: t.LogoURL});
    h2hTeam.value = data.team.ID;
    root.querySelector('[data-h2h-controls] input[name="team"]').value = data.team.ID;
    const counts = new Map();
    h2hData.fixtures.forEach(f => {
      const opponent = f.home === teamIndex ? f.away : f.away === teamIndex ? f.home : null;
      if (opponent !== null) counts.set(opponent, (counts.get(opponent) || 0) + 1);
    });
    // The shared team list is already in newest-name / ID order.
    const pairs = h2hData.teams.flatMap((opponent, index) => counts.has(index) ? [{opponent, all: {Played: counts.get(index)}, index}] : []);
    let pair = pairs.find(p => p.opponent.ID === params.get('opponent'));
    if (!pair && params.has('opponent')) {
      const index = h2hData.teams.findIndex(t => t.ID === params.get('opponent'));
      if (index >= 0) pair = {opponent: h2hData.teams[index], all: {Played: 0}, index};
    }
    if (!pair) pair = pairs.reduce((best, p) => !best || p.all.Played > best.all.Played ? p : best, null);
    if (!pair) return;
    const rows = h2hData.fixtures.filter(f => (f.home === teamIndex && f.away === pair.index) || (f.away === teamIndex && f.home === pair.index)).map((f, position) => {
      const home = f.home === teamIndex, season = h2hData.seasons[f.season];
      const orient = values => home ? values : [values[1], values[0]];
      const score = orient(f.score), margin = score[0] - score[1];
      const goals = np ? f.npGoals ? orient(f.npGoals) : [null, null] : score;
      const xg = orient(np ? f.npxg : f.xg), xpoints = f.xpoints[home ? 0 : 1];
      return {...f, season: season.year, active: !!season.active, position, venue: home ? 'Home' : 'Away', opponent: season.names[pair.opponent.ID] || pair.opponent.ID,
        pointsView, both, np, xgLabel, basisGoals: goals, xg, xpoints,
        goalsCovered: !np || f.npGoals !== null,
        values: [{actual: goals[0], expected: xg[0]}, {actual: goals[1], expected: xg[1]},
          {actual: goals[0] === null ? null : goals[0] - goals[1], expected: xg.some(v => v === null) ? null : xg[0] - xg[1]},
          {actual: margin > 0 ? 3 : margin < 0 ? 0 : 1, expected: xpoints}], expectedMargin: xg.every(v => v !== null) ? xg[0] - xg[1] : null,
        score: score.join('–'), goalsFor: score[0], goalsAgainst: score[1], margin, result: margin > 0 ? 'Win' : margin < 0 ? 'Loss' : 'Draw', points: margin > 0 ? 3 : margin < 0 ? 0 : 1};
    });
    const years = [...new Set(rows.map(r => r.season))].sort();
    const activeSeason = rows.find(r => r.active)?.season;
    pair.scope = rows.length ? `Regular season ${years[0]}${years.length > 1 ? '–' + years.at(-1) : ''} · ${rows.length} ${rows.length === 1 ? 'meeting' : 'meetings'}${activeSeason ? ` · ${activeSeason} in progress` : ''}` : 'Regular season';
    pair.warning = `${rows.some(r => r.undated) ? 'Undated matches appear last. ' : ''}${rows.some(r => r.venueDateUnavailable) ? 'Some venue-local dates are unavailable.' : ''}`.trim();
    const choices = pairs.includes(pair) ? pairs : [...pairs, pair];
    h2hOpponent.replaceChildren(...choices.map(p => {
      const option = archiveElement('option', `${p.opponent.Name} (${p.all.Played})`); option.value = p.opponent.ID; return option;
    }));
    h2hOpponent.value = pair.opponent.ID;
    const title = root.querySelector('[data-h2h-title]');
    title.replaceChildren(teamName(identity(data.team)), document.createTextNode(' vs '), teamName(identity(pair.opponent)));
    for (const key of ['scope', 'warning']) {
      const element = root.querySelector(`[data-h2h-${key}]`); element.textContent = pair[key];
      if (key === 'warning') element.hidden = !pair.warning;
    }
    const record = root.querySelector('[data-h2h-record]');
    record.setAttribute('aria-label', `Official regular-season record from ${data.team.Name}’s side`);
    showMarginTiles(record, rows, np, margins, 'meeting');
    showPointsTiles(root.querySelector('[data-h2h-points-tiles]'), rows, 'meeting', pointsView);
    showGoalGapTiles(root.querySelector('[data-h2h-comparison]'), rows, np, 'meeting', both);
    const goalsCovered = rows.filter(r => r.basisGoals[0] !== null), xgCovered = rows.filter(r => r.xg[0] !== null);
    const coverage = root.querySelector('[data-h2h-coverage]'), notes = [];
    const missingNote = (n, metric) => `${n} ${n === 1 ? 'meeting lacks' : 'meetings lack'} ${metric}.`;
    if (!pointsView && xgCovered.length < rows.length) notes.push(missingNote(rows.length - xgCovered.length, xgLabel));
    if (np && goalsCovered.length < rows.length) notes.push(`Non-penalty goals and npxG cover ${goalsCovered.length} of ${rows.length} meetings with a penalty split.`);
    coverage.textContent = notes.join(' '); coverage.hidden = !notes.length;
    root.querySelector('[data-h2h-official]').hidden = !margins || !np;
    const empty = root.querySelector('[data-h2h-empty]'); empty.hidden = pair.all.Played > 0;
    empty.textContent = `${data.team.Name} and ${pair.opponent.Name} have not met in a regular-season match.`;
    root.querySelector('[data-h2h-chart-wrap]').hidden = !pair.all.Played;
    root.querySelector('[data-h2h-summary]').textContent = `Table: all ${pair.all.Played} meetings`;
    const expanded = params.has('h2h-sort') || params.has('h2h-order');
    root.querySelector('[data-h2h-values]').open = expanded;
    root.querySelectorAll('[data-h2h-controls] input[name="h2h-sort"], [data-h2h-controls] input[name="h2h-order"]').forEach(input => {
      input.disabled = !expanded; input.value = params.get(input.name) || (input.name === 'h2h-sort' ? 'date' : 'desc');
    });
    const column = params.get('h2h-sort') || 'date', order = params.get('h2h-order') || 'desc';
    const value = row => column === 'date' ? row.position : column === 'result' ? row.points : row[column];
    const sorted = [...rows].sort((a, b) => {
      if (!!a.undated !== !!b.undated) return a.undated ? 1 : -1;
      const x = value(a), y = value(b);
      if (x !== y) return (x < y ? -1 : 1) * (order === 'asc' ? 1 : -1);
      return b.position - a.position;
    });
    root.querySelector('[data-h2h-rows]').replaceChildren(...sorted.map(row => {
      const tr = archiveElement('tr'); tr.dataset.matchId = row.id;
      for (const text of [row.date, `${row.season}${row.active ? ' (season in progress)' : ''}`, row.venue, row.opponent, row.score, row.result, row.points]) tr.append(archiveElement('td', text));
      const basisCell = archiveElement('td', row.basisGoals[0] === null ? 'Unavailable' : row.basisGoals.join('–')); basisCell.dataset.h2hBasisColumn = ''; basisCell.hidden = !np; tr.append(basisCell);
      [...row.xg, row.xpoints].forEach((value, i) => { const td = archiveElement('td', value === null ? 'Unavailable' : fixed(value)); td.dataset.h2hExpectedColumn = ''; td.hidden = pointsView ? i < 2 : i === 2; tr.append(td); });
      const gapCell = archiveElement('td', pointsView ? pointGapText(row) : both ? goalSideGapText(row, 0) : row.expectedMargin === null ? 'Unavailable' : signed(row.expectedMargin)); gapCell.dataset.h2hExpectedColumn = ''; tr.append(gapCell);
      const allowed = archiveElement('td', goalSideGapText(row, 1)); allowed.dataset.h2hExpectedColumn = ''; allowed.hidden = !both; tr.append(allowed);
      return tr;
    }));
    root.querySelectorAll('[data-h2h-sort]').forEach(link => {
      const active = link.dataset.h2hSort === column;
      link.parentElement.setAttribute('aria-sort', active ? order === 'asc' ? 'ascending' : 'descending' : 'none');
      const target = new URLSearchParams(params);
      target.set('view', 'head-to-head'); target.set('team', data.team.ID); target.set('opponent', pair.opponent.ID);
      target.set('h2h-sort', link.dataset.h2hSort); target.set('h2h-order', active && order === 'asc' ? 'desc' : 'asc');
      link.href = `?${target}`;
    });
    // The same labels remain available to assistive technology and become the
    // chronological chart fallback when JavaScript is off.
    const fallback = root.querySelector('[data-h2h-fallback]');
    fallback.classList.add('visually-hidden');
    fallback.replaceChildren(...rows.map(row => {
      const label = headToHeadDescription(row), li = archiveElement('li', label);
      li.dataset.h2hMark = row.id; li.dataset.margin = row.margin;
      li.dataset.venue = row.venue; li.dataset.encoding = row.venue === 'Home' ? 'solid-blue' : 'striped-orange';
      li.setAttribute('aria-label', label); return li;
    }));
    const wrap = root.querySelector('[data-h2h-chart]'); wrap.hidden = !rows.length;
    if (!rows.length) return;
    root.querySelector('[data-h2h-xg-chart]').hidden = pointsView;
    if (!pointsView) {
      showMatchBarPanels({panels: [{canvas: 'head-to-head', key: 'headToHead'}, {canvas: 'head-to-head-xg', key: 'headToHeadXG'}], matches: rows, description: `${data.team.Name} vs ${pair.opponent.Name}`, nonPenalty: np, matchNumbers: false, goalGaps: both});
      return;
    }
    const canvas = wrap.querySelector('canvas');
    canvas.setAttribute('aria-label', `${data.team.Name} vs ${pair.opponent.Name}: ${'points minus xPoints; above zero: more points than xPoints; below zero: fewer'}; home solid blue, away striped orange`);
    if (!charts.headToHead) charts.headToHead = createHeadToHead(canvas);
    const chart = charts.headToHead;
    delete chart.options.scales.y.afterFit;
    chart.meetings = rows;
    chart.data.labels = rows.map(row => row.id);
    chart.data.datasets[0].data = rows.map(row => window.NWSLGeometry.pointsGap(row.points, row.xpoints));
    chart.data.datasets[0].label = 'Points − xPoints';
    pointsGapAxis(chart);
    chart.update('none');
  }
  const goalSideGapText = (row, side) => { const gap = window.NWSLGeometry.goalGap(row.basisGoals, row.xg, side); return gap === null ? 'Unavailable' : signed(gap); };
  const goalGapDescription = row => [
    `${row.np ? 'Non-penalty goals' : 'Goals'}: ${row.basisGoals[0] === null ? 'Unavailable' : row.basisGoals.join('–')}`,
    `${row.xgLabel} for: ${row.xg[0] === null ? 'Unavailable' : fixed(row.xg[0])}`,
    `${row.xgLabel} against: ${row.xg[1] === null ? 'Unavailable' : fixed(row.xg[1])}`,
    `${archiveText('Goals for − xG for', row.np)}: ${goalSideGapText(row, 0)}`,
    `${archiveText('Goals against − xG against', row.np)}: ${goalSideGapText(row, 1)}`];
  const pointGapText = row => row.xpoints == null ? 'Unavailable' : signed(row.points - row.xpoints);
  const pointsDescription = row => [`Points: ${row.points}`, `xPoints: ${row.xpoints == null ? 'Unavailable' : fixed(row.xpoints)}`, `Points − xPoints: ${pointGapText(row)}`];
  const goalMarginText = row => row.basisGoals[0] === null ? 'Unavailable' : `${row.basisGoals[0] > row.basisGoals[1] ? '+' : ''}${row.basisGoals[0] - row.basisGoals[1]}`;
  const headToHeadExpectedDescription = row => row.pointsView ? pointsDescription(row) : row.both ? goalGapDescription(row) : [
    ...(row.np ? [`Non-penalty goals: ${row.basisGoals[0] === null ? 'Unavailable' : row.basisGoals.join('–')}`] : []),
    `${row.np ? 'Non-penalty goal margin' : 'Goal margin'}: ${goalMarginText(row)}`,
    `${row.xgLabel} for: ${row.xg[0] === null ? 'Unavailable' : fixed(row.xg[0])}`,
    `${row.xgLabel} against: ${row.xg[1] === null ? 'Unavailable' : fixed(row.xg[1])}`,
    `${row.xgLabel} margin: ${row.expectedMargin === null ? 'Unavailable' : signed(row.expectedMargin)}`,

  ];
  const headToHeadDescription = row => [`${row.date}${row.active ? ' · season in progress' : ''} · ${row.venue} · ${row.opponent} · ${row.score} · ${row.result}`, ...headToHeadExpectedDescription(row)].join(' · ');
  function headToHeadInspection(row) {
    return {title: row.date, lines: [...(row.active ? ['season in progress'] : []), row.venue, row.opponent, `${row.score} · ${row.result}`, ...headToHeadExpectedDescription(row)]};
  }
  // An opponent-adjusted row carries its official match: tooltips show the
  // official value, then the adjusted one, then the factors.
  const besideAdjusted = (row, read, format, adjustedFormat = format) => {
    const text = (value, as) => value === null ? 'Unavailable' : as(value);
    const official = text(read(row.official || row), format);
    return row.official ? `${official} · adjusted ${text(read(row), adjustedFormat)}` : official;
  };
  // Results stay official; an adjusted row's points are shifted.
  const matchResult = row => ({3: 'Win', 1: 'Draw', 0: 'Loss'}[(row.official || row).values[matchMeasure.points].actual]);
  function seasonPointsInspection(row, index) {
    const points = match => match.values[matchMeasure.points];
    return {title: `Match ${index + 1} · ${row.date}`, lines: [row.venue, row.opponent, `${row.score} · ${matchResult(row)}`,
      `Points: ${besideAdjusted(row, match => points(match).actual, String, fixed)}`,
      `xPoints: ${besideAdjusted(row, match => points(match).expected ?? null, fixed)}`,
      ...(row.official ? [`Average team's expected points: ${row.opponents ? `${fixed(row.opponents.expectedPoints)} (season average ${fixed(row.averagePoints)})` : 'Unavailable'}`] : [])]};
  }
  function seasonMarginInspection(row, index) {
    const goals = marginsData.goals(row.official || row);
    const result = matchResult(row);
    const label = T('xG'), whole = value => `${value > 0 ? '+' : ''}${value}`;
    return {title: `Match ${index + 1} · ${row.date}`, lines: [row.venue, row.opponent, `${row.score} · ${result}`,
      ...(nonPenalty ? [`Non-penalty goals: ${goals[0] === null ? 'Unavailable' : goals.join('–')}`] : []),
      `${T('Goal margin')}: ${besideAdjusted(row, marginsData.goalMargin, whole, signed)}`,
      `${label} for: ${besideAdjusted(row, match => marginsData.xg(match)[0], fixed)}`, `${label} against: ${besideAdjusted(row, match => marginsData.xg(match)[1], fixed)}`,
      `${label} margin: ${besideAdjusted(row, marginsData.xgMargin, signed)}`, ...opponentFactorLines(row)]};
  }
  function seasonBalanceInspection(row, index) {
    const goals = marginsData.goals(row.official || row);
    const result = matchResult(row);
    return {title: `Match ${index + 1} · ${row.date}`, lines: [row.venue, row.opponent, `${row.score} · ${result}`,
      ...(nonPenalty ? [`Non-penalty goals: ${goals[0] === null ? 'Unavailable' : goals.join('–')}`] : []),
      ...(row.official ? [0, 1].map(side => `${T('Goals')} ${side ? 'against' : 'for'}: ${besideAdjusted(row, match => marginsData.goals(match)[side], String, fixed)}`) : []),
      `${T('xG')} for: ${besideAdjusted(row, match => marginsData.xg(match)[0], fixed)}`, `${T('xG')} against: ${besideAdjusted(row, match => marginsData.xg(match)[1], fixed)}`,
      ...opponentFactorLines(row)]};
  }
  function seasonGoalGapInspection(row, index) {
    const result = matchResult(row);
    return {title: `Match ${index + 1} · ${row.date}`, lines: [row.venue, row.opponent, `${row.score} · ${result}`,
      ...goalGapDescription({basisGoals: marginsData.goals(row), xg: marginsData.xg(row), xgLabel: T('xG'), np: nonPenalty})]};
  }
  // Use Chart.js's event and tooltip lifecycle, with the full-height nearest
  // column geometry shared by pointer, touch and geometry tests.
  Chart.Interaction.modes.headToHead = (chart, event) => {
    const columns = chart.getDatasetMeta(0).data.map(bar => ({center: bar.x}));
    const index = window.NWSLGeometry.headToHeadHit(columns, chart.chartArea, event);
    return index === null ? [] : [{element: chart.getDatasetMeta(0).data[index], datasetIndex: 0, index}];
  };
  Chart.Tooltip.positioners.headToHead = function(items) {
    if (!items.length) return false;
    const bar = items[0].element;
    return {x: bar.x, y: bar.y, xAlign: bar.x < this.chart.width / 2 ? 'left' : 'right', yAlign: 'center'};
  };
  function venueBarStyle(canvas) {
    // A high-resolution tile keeps the prescribed 3.4px spacing and 1.5px
    // stripe width at 45 degrees at every device pixel ratio.
    const tile = document.createElement('canvas'); tile.width = tile.height = Math.round(340 * Math.SQRT2);
    const ctx = tile.getContext('2d');
    ctx.fillStyle = color('--paper'); ctx.fillRect(0, 0, tile.width, tile.height);
    ctx.strokeStyle = '#CF7424'; ctx.lineWidth = 150;
    for (const offset of [-tile.width, 0, tile.width]) {
      ctx.beginPath(); ctx.moveTo(offset - tile.width, -tile.height); ctx.lineTo(offset + 2 * tile.width, 2 * tile.height); ctx.stroke();
    }
    const stripes = canvas.getContext('2d').createPattern(tile, 'repeat'); stripes.setTransform(new DOMMatrix().scale(.01));
    const away = context => context.chart.meetings?.[context.dataIndex]?.venue === 'Away';
    const fill = context => away(context) ? stripes : '#1F6FA8';
    const outline = context => away(context) ? 1 : 0;
    return {backgroundColor: fill, hoverBackgroundColor: fill, borderColor: '#CF7424', hoverBorderColor: '#CF7424', borderWidth: outline, hoverBorderWidth: outline, borderSkipped: false, barPercentage: .7, categoryPercentage: 1, minBarLength: 3};
  }
  // Opponent logos are decorative (inspection names the opponent), so a logo
  // that fails to load leaves its slot blank. Images are shared by URL.
  const opponentLogoImages = new Map();
  function opponentLogoImage(url, chart) {
    let image = opponentLogoImages.get(url);
    if (!image) {
      image = new Image(); image.decoding = 'async'; image.waiting = new Set();
      image.addEventListener('load', () => { image.waiting.forEach(waiting => waiting.draw()); image.waiting.clear(); });
      image.addEventListener('error', () => image.waiting.clear());
      image.src = url;
      opponentLogoImages.set(url, image);
    }
    if (!image.complete) image.waiting.add(chart);
    return image;
  }
  // Show the logo row only when every match slot is wide enough for a logo:
  // thinning logos would leave unlabeled matches. The wrap grows by the row's
  // height so the plot keeps its height.
  function syncOpponentLogoRow(chart, width = chart.canvas.parentElement.clientWidth) {
    const count = chart.opponentLogos?.length || 0;
    // The y axis and right padding take about 48px of the canvas.
    const shown = count > 0 && (width - 48) / count >= 16;
    chart.opponentLogoRow = shown;
    chart.options.layout.padding.bottom = shown ? 56 : 28;
    chart.canvas.parentElement.classList.toggle('has-opponent-logos', shown);
  }
  function drawOpponentLogos(chart) {
    chart.opponentLogoRects = [];
    if (!chart.opponentLogoRow) return;
    const {chartArea: area, ctx} = chart, x = chart.scales.x;
    const size = Math.min(20, Math.floor(area.width / chart.opponentLogos.length) - 4);
    chart.opponentLogos.forEach((url, index) => {
      const image = url && opponentLogoImage(url, chart);
      if (!image?.complete || !image.naturalWidth) return;
      // Contain the logo in a size x size box centered under its slot.
      const scale = size / Math.max(image.naturalWidth, image.naturalHeight);
      const width = image.naturalWidth * scale, height = image.naturalHeight * scale;
      const rect = {index, x: x.getPixelForValue(index) - width / 2, y: area.bottom + 6 + (size - height) / 2, width, height};
      ctx.drawImage(image, rect.x, rect.y, width, height);
      chart.opponentLogoRects.push(rect);
    });
  }
  function createHeadToHead(canvas, matchNumbers = false, inspection = headToHeadInspection) {
    const options = commonOptions();
    options.onResize = (chart, size) => syncOpponentLogoRow(chart, size.width);
    options.interaction = {mode: 'headToHead', intersect: false};
    options.layout = {padding: {top: 12, right: 8, bottom: 28}};
    options.scales = {
      x: {offset: true, grid: {display: false}, ticks: {display: false}},
      y: {min: -1, max: 1, grid: {color: c => c.tick.value === 0 ? color('--muted') : color('--line')}, ticks: {maxTicksLimit: 5, precision: 0}},
    };
    options.plugins.tooltip.position = 'headToHead';
    options.plugins.tooltip.padding = 8;
    options.plugins.tooltip.titleFont = {size: 11}; options.plugins.tooltip.bodyFont = {size: 11};
    options.plugins.tooltip.callbacks = {
      title: items => items.length ? items[0].chart.inspection(items[0].chart.meetings[items[0].dataIndex], items[0].dataIndex).title : '',
      label: item => item.chart.inspection(item.chart.meetings[item.dataIndex], item.dataIndex).lines,
    };
    // afterDatasetsDraw runs before Chart.js draws the tooltip, so the
    // tooltip stays above the labels and logos below the plot.
    const years = {id: 'headToHeadYears', afterDatasetsDraw(chart) {
      const rows = chart.meetings || [], seen = new Set(), ctx = chart.ctx;
      ctx.save(); ctx.font = '11px system-ui, sans-serif'; ctx.fillStyle = color('--muted'); ctx.textAlign = 'center';
      const candidates = rows.flatMap((row, index) => {
        const text = matchNumbers ? String(index + 1) : row.season;
        if (!matchNumbers && seen.has(text)) return []; seen.add(text);
        return [{text, center: chart.getDatasetMeta(0).data[index].x, width: ctx.measureText(text).width}];
      });
      chart.yearLabels = window.NWSLGeometry.headToHeadLabels(candidates, chart.chartArea);
      // Match numbers sit below the opponent logo row when it is shown.
      const baseline = chart.chartArea.bottom + (chart.opponentLogoRow ? 46 : 20);
      chart.yearLabelBaseline = baseline;
      chart.yearLabels.forEach(label => ctx.fillText(label.text, label.center, baseline)); ctx.restore();
      drawOpponentLogos(chart);
    }};
    // Focus can scroll the page before Chart.js processes an earlier tap.
    // Those stale events and the resulting mouseout must not clear keyboard inspection.
    const keyboardTooltip = {id: 'headToHeadKeyboard', beforeEvent(chart, {event, replay}) {
      if (chart.keyboardInspecting && (event.type === 'mouseout' || replay || event.native?.timeStamp < chart.keyboardInspectionStartedAt)) return false;
      chart.keyboardInspecting = false;
    }};
    const chart = new Chart(canvas, {type: 'bar', options, plugins: [years, keyboardTooltip], data: {labels: [], datasets: [{label: 'Goal margin', data: [], ...venueBarStyle(canvas)}]}});
    chart.inspection = inspection;
    chart.meetings = [];
    // Every meeting has an inspectable slot, including those without xG or splits.
    chart.keyboardIncludeMissing = true;
    chart.keyboardByIndex = true;
    keyboardAccess(chart, mark => { const detail = chart.inspection(chart.meetings[mark.index], mark.index); return [detail.title,...detail.lines].join(' · '); }, true);
    return chart;
  }
  const changeHeadToHead = event => {
    const changes = {view: 'head-to-head', team: h2hTeam.value, opponent: event.target === h2hTeam || event.target.hasAttribute('data-h2h-team-form') ? null : h2hOpponent.value, 'h2h-series': h2hSeries.value === 'actual' ? null : h2hSeries.value};
    if (event.target === h2hPenalties) changes.penalties = h2hPenalties.checked ? 'exclude' : null;
    update(changes);
  };
  root.querySelectorAll('[data-h2h-controls]').forEach(form => form.addEventListener('submit', event => { event.preventDefault(); changeHeadToHead(event); }));
  h2hTeam.addEventListener('change', changeHeadToHead);
  h2hOpponent.addEventListener('change', changeHeadToHead);
  root.querySelector('[data-segmented="h2h-series"]').addEventListener('change', changeHeadToHead);
  h2hPenalties.addEventListener('change', changeHeadToHead);


  function applyURL() {
    const params = new URL(location.href).searchParams;
    const requested = requestedView(params);
    const view = views.has(requested) ? requested : viewGroups[0].views[0].key;
    // Some requests skip forecast work. Entering a view that needs its
    // forecast benchmark payload takes a normal server render.
    if (views.get(root.dataset.view)?.skipsForecast && views.get(view).needsForecast) { location.assign(location.href); return; }
    if (isPointsOnlySelection(params) && params.get('penalties') === 'exclude') {
      params.delete('penalties');
      history.replaceState(null, '', `?${params}`);
      if (!useBasis(false)) { location.assign(location.href); return; }
    }
    // Server-rendered text names the rendered basis, so only a view redrawn
    // from the payloads shows another one.
    const wanted = params.get('penalties') === 'exclude';
    if (wanted !== renderedNonPenalty && !views.get(view).bothBases && !switchesBasis(params)) { location.assign(location.href); return; }
    if (!views.get(view).bothBases && !useBasis(wanted)) { location.assign(location.href); return; }
    nonPenalty = params.get('penalties') === 'exclude';
    labelBasis();
    // Server-rendered text on both bases follows the named basis (CSS shows
    // data-basis-only content for it); accessible names carry both variants.
    root.dataset.basis = nonPenalty ? 'non-penalty' : 'all';
    root.querySelectorAll('[data-penalties-selector]').forEach(checkbox => { checkbox.checked = nonPenalty; });
    // Every in-page link carries the penalties selection, including links in
    // views the server rendered on the other basis.
    root.querySelectorAll('a[href^="?"]').forEach(link => {
      const selection = new URLSearchParams(link.getAttribute('href').slice(1));
      if ((selection.get('penalties') === 'exclude') === nonPenalty) return;
      if (nonPenalty) selection.set('penalties', 'exclude'); else selection.delete('penalties');
      link.setAttribute('href', `?${selection}`);
    });
    root.querySelectorAll('[data-aria-all]').forEach(element => element.setAttribute('aria-label', nonPenalty ? element.dataset.ariaNp : element.dataset.ariaAll));
    styleTable();
    if (view === 'head-to-head' && ['actual', 'expected'].includes(params.get('h2h-series'))) { params.delete('h2h-series'); history.replaceState(null, '', `?${params}`); }
    dismiss();
    root.querySelectorAll('[data-panel]').forEach(panel => { panel.hidden = panel.dataset.panel !== view; });
    // Views with their own penalties control render no generic selector.
    const select = root.querySelector(`[data-panel="${view}"] [data-penalties-selector]`);
    if (select) select.closest('label').hidden = isPointsOnlySelection(params);
    const {group} = views.get(view);
    root.querySelectorAll('[data-group-views]').forEach(nav => { nav.hidden = nav.dataset.groupViews !== group; });
    root.querySelectorAll('[data-team-display]').forEach(link => {
      const selected = view === 'teams' && link.dataset.teamDisplay ===
        (teamDisplays.includes(params.get('display')) ? params.get('display') : teamDisplays[0]);
      if (selected) link.setAttribute('aria-current', 'page'); else link.removeAttribute('aria-current');
      link.href = teamLink({display: link.dataset.teamDisplay});
    });
    root.querySelectorAll('[data-group-choice]').forEach(link => {
      if (link.dataset.groupChoice === group) link.setAttribute('aria-current', 'page');
      else link.removeAttribute('aria-current');
    });
    root.querySelectorAll('[data-view-choice]').forEach(link => {
      if (link.dataset.viewChoice === view) link.setAttribute('aria-current', 'page');
      else link.removeAttribute('aria-current');
    });
    const currentTab = root.querySelector('[data-group-views]:not([hidden]) [aria-current]');
    if (currentTab) document.title = `${currentTab.textContent} · ${titleSuffix}`;
    metric.value = ['goals', 'xg', 'compare', 'gap'].includes(params.get('metric')) ? params.get('metric') : 'compare';
    root.querySelector('#explore-trend-title').textContent = metric.value === 'gap'
      ? T('Goals − xG per match') : T('Goals and chances per match');
    root.querySelectorAll('[data-series]').forEach(series => {
      series.hidden = metric.value === 'gap' || (metric.value !== 'compare' && series.dataset.series !== metric.value);
    });
    if (distributionBin) {
      distributionBin.value = ['all', '0', '1', '2', '3', '4'].includes(params.get('distribution-bin'))
        ? params.get('distribution-bin') : 'all';
      root.querySelector('#explore-distribution-title').textContent = distributionBin.value === 'all'
        ? `Share of regular-season matches by total ${goalsWord}`
        : `Share of regular-season matches with ${binLabels[Number(distributionBin.value)]}`;
      root.querySelector('[data-distribution-stacked-panel]').hidden = distributionBin.value !== 'all';
      root.querySelector('[data-distribution-trend-panel]').hidden = distributionBin.value === 'all';
    }
    const canvas = root.querySelector(`[data-chart="${view}"]`);
    if (view === 'trend' && canvas && !charts.trend) charts.trend = createLeagueTrend(canvas);
    if (view === 'distribution' && distributionBin) {
      if (distributionBin.value === 'all') {
        if (!charts.distribution) charts.distribution = createDistribution(canvas);
        charts.distribution.resize(); charts.distribution.update('none');
      } else {
        const trendCanvas = root.querySelector('[data-chart="bin-trend"]');
        const chart = charts.binTrend || (charts.binTrend = createBinTrend(trendCanvas));
        showBinTrend(chart, Number(distributionBin.value));
      }
    }
    if (view === 'home-advantage') showHomeAdvantage();
    if (view === 'parity') showParity(params);
    if (view === 'thresholds') showThresholds(params);
    if (view === 'team-seasons') showTeamSeasons(params);
    if (view === 'head-to-head') showHeadToHead(params);
    if (view === 'teams') showTeams(params);
    if (view === 'team-history') showTeamHistory(params);
    if (view === 'season-trend') showSeasonTrend(params);
    if (view === 'team-rankings') showTeamRankings(params);
    root.querySelectorAll('[data-view-choice], [data-group-choice]').forEach(link => {
      const target = link.dataset.viewChoice || viewGroups.find(group => group.key === link.dataset.groupChoice).views[0].key;
      const selection = new URLSearchParams(params); selection.set('view', target);
      link.href = `?${selection}`;
    });
    if (view === 'trend' && charts.trend) {
      const chart = charts.trend;
      const gaps = chart.data.datasets[2].data.filter(value => value !== null);
      const gapEmpty = metric.value === 'gap' && gaps.length === 0;
      chart.data.datasets.forEach((series, index) => chart.setDatasetVisibility(index,
        metric.value === 'compare' ? series.key !== 'gap' : series.key === metric.value));
      // Reserve half a category at each edge so the first and last bars are not clipped.
      chart.options.scales.x.offset = metric.value === 'gap';
      chart.options.scales.y = trendYAxis(metric.value === 'gap' ? gaps : null);
      chart.canvas.setAttribute('aria-label', {
        compare: T('Goals and expected goals per regular-season match, by season'),
        goals: T('Goals per regular-season match, by season'),
        xg: T('Expected goals per regular-season match, by season'),
        gap: T('Goals minus expected goals per regular-season match, by season; positive bars are above xG and negative bars are below xG'),
      }[metric.value]);
      root.querySelector('[data-trend-gap-legend]').hidden = metric.value !== 'gap' || gapEmpty;
      root.querySelector('[data-trend-gap-note]').hidden = metric.value !== 'gap' || gapEmpty;
      root.querySelector('[data-trend-gap-empty]').hidden = !gapEmpty;
      root.querySelector('[data-trend-chart-wrap]').hidden = gapEmpty;
      root.querySelector('[data-trend-keyboard-hint]').hidden = gapEmpty;
      chart.resize();
      chart.update('none');
    }
    const column = Number(params.get('sort') || 0);
    sortTable(Number.isInteger(column) && column >= 0 && column <= 4 ? column : 0, params.get('order') === 'asc' ? 'asc' : 'desc');
    sortDistributionTable(params);
    sortHomeTable(params);
    sortParityTable(params);
  }
  function update(changes) {
    const url = new URL(location.href);
    Object.entries(changes).forEach(([key, value]) => { if (value === null) url.searchParams.delete(key); else url.searchParams.set(key, value); });
    const becomingPointsOnly = isPointsOnlySelection(url.searchParams);
    if (becomingPointsOnly) url.searchParams.delete('penalties');
    if (becomingPointsOnly && !useBasis(false)) { location.assign(url); return; }
    if (url.searchParams.get('view') === 'season-trend') {
      const params = url.searchParams;
      if (!trendSortAvailable(params)) {
        params.delete('trend-sort'); params.delete('trend-order');
      }
    }
    history.pushState(null, '', url);
    applyURL();
  }
  root.addEventListener('click', event => {
    const trendSort = event.target.closest('[data-trend-sort]');
    if (trendSort && !event.ctrlKey && !event.metaKey && !event.shiftKey && !event.altKey) {
      event.preventDefault();
      const params = new URL(trendSort.href).searchParams;
      update({'trend-sort': params.get('trend-sort'), 'trend-order': params.get('trend-order')});
    }
    const h2hSort = event.target.closest('[data-h2h-sort]');
    if (h2hSort && !event.ctrlKey && !event.metaKey && !event.shiftKey && !event.altKey) {
      event.preventDefault();
      const params = new URL(h2hSort.href).searchParams;
      update({view: 'head-to-head', 'h2h-sort': params.get('h2h-sort'), 'h2h-order': params.get('h2h-order')});
    }
    const archiveDisplay = event.target.closest('[data-team-seasons-display]');
    if (archiveDisplay && !event.ctrlKey && !event.metaKey && !event.shiftKey && !event.altKey) {
      event.preventDefault();
      update({view: 'team-seasons', 'team-seasons-display': archiveDisplay.dataset.teamSeasonsDisplay === 'ranking' ? null : 'scatter'});
    }
    const archiveSort = event.target.closest('[data-team-seasons-sort]');
    if (archiveSort && !event.ctrlKey && !event.metaKey && !event.shiftKey && !event.altKey) {
      event.preventDefault();
      const params = new URL(archiveSort.href).searchParams;
      update({view: 'team-seasons', 'team-seasons-sort': params.get('team-seasons-sort'), 'team-seasons-order': params.get('team-seasons-order')});
    }
    const thresholdLink = event.target.closest('[data-panel="thresholds"] a[href^="?view=thresholds"]');
    if (thresholdLink && !event.ctrlKey && !event.metaKey && !event.shiftKey && !event.altKey) {
      event.preventDefault();
      const selection = new URL(thresholdLink.getAttribute('href'), location.href).searchParams;
      if (thresholdLink.hasAttribute('data-threshold-sort')) {
        update({view: 'thresholds', 'threshold-outcome': selection.get('threshold-outcome'), 'threshold-sort': selection.get('threshold-sort'), 'threshold-order': selection.get('threshold-order')});
      } else update({view: 'thresholds', 'threshold-season': selection.get('threshold-season')});
    }
    const paritySort = event.target.closest('[data-parity-sort]');
    if (paritySort && !event.ctrlKey && !event.metaKey && !event.shiftKey && !event.altKey) {
      event.preventDefault();
      const params = new URL(paritySort.href).searchParams;
      update({view: 'parity', 'parity-sort': params.get('parity-sort'), 'parity-order': params.get('parity-order')});
    }
    const parityLink = event.target.closest('[data-parity-season-link]');
    if (parityLink && !event.ctrlKey && !event.metaKey && !event.shiftKey && !event.altKey) {
      event.preventDefault();
      selectParitySeason(parityLink.dataset.paritySeasonLink);
    }
    const homeLink = event.target.closest('[data-home-sort]');
    if (homeLink && !event.ctrlKey && !event.metaKey && !event.shiftKey && !event.altKey) {
      event.preventDefault();
      const params = new URL(homeLink.href).searchParams;
      update({view: 'home-advantage', 'home-sort': params.get('home-sort'), 'home-order': params.get('home-order')});
    }
    const distributionLink = event.target.closest('[data-distribution-sort]');
    if (distributionLink && !event.ctrlKey && !event.metaKey && !event.shiftKey && !event.altKey) {
      event.preventDefault();
      const params = new URL(distributionLink.href).searchParams;
      update({view: 'distribution', 'distribution-sort': params.get('distribution-sort'), 'distribution-order': params.get('distribution-order')});
    }
    const historyLink = event.target.closest('[data-history-sort]');
    if (historyLink && !event.ctrlKey && !event.metaKey && !event.shiftKey && !event.altKey) {
      event.preventDefault();
      const params = new URL(historyLink.href).searchParams;
      update({'history-sort': params.get('history-sort'), 'history-order': params.get('history-order')});
    }
    const teamLink = event.target.closest('[data-team-display], [data-team-sort]');
    if (teamLink && !event.ctrlKey && !event.metaKey && !event.shiftKey && !event.altKey) {
      event.preventDefault();
      const params = new URL(teamLink.href).searchParams;
      const changes = {view: 'teams', display: params.get('display')};
      if (teamLink.hasAttribute('data-team-sort')) {
        changes['team-sort'] = params.get('team-sort'); changes['team-order'] = params.get('team-order');
      }
      update(changes);
    }
    const link = event.target.closest('[data-view-choice], [data-group-choice]');
    if (link && !event.ctrlKey && !event.metaKey && !event.shiftKey && !event.altKey) {
      event.preventDefault();
      const selection = new URL(link.href).searchParams;
      const changes = {view: selection.get('view')};
      update(changes);
    }
    const sort = event.target.closest('[data-sort]');
    if (sort) update({sort: sort.dataset.sort, order: sort.parentElement.getAttribute('aria-sort') === 'descending' ? 'asc' : 'desc'});
  });
  document.addEventListener('pointerdown', event => { if (!event.target.closest('[data-chart]')) dismiss(false); });
  document.addEventListener('keydown', event => { if (event.key === 'Escape') dismiss(); });
  const thresholdControls = root.querySelector('[data-threshold-controls]');
  const changeThresholdOutcome = () => update({view: 'thresholds', 'threshold-outcome': thresholdControls.elements['threshold-outcome'].value});
  thresholdControls.addEventListener('change', changeThresholdOutcome);
  thresholdControls.addEventListener('submit', event => { event.preventDefault(); changeThresholdOutcome(); });
  metricControl?.addEventListener('change', () => update({view: 'trend', metric: metric.value}));
  if (distributionBin) {
    root.querySelector('[data-panel="distribution"] [data-segmented="distribution-bin"]').addEventListener('change', () => update({'distribution-bin': distributionBin.value}));
    distributionControls.addEventListener('submit', event => {
      event.preventDefault(); update({'distribution-bin': distributionBin.value});
    });
  }
  teamControls.addEventListener('submit', event => {
    event.preventDefault(); update({season: teamSeason.value, measure: teamMeasure.value, units: teamUnits.value, 'quadrant-data': quadrantData.value});
  });
  // The chart card's controls sit outside the form but belong to it. A new
  // measure keeps the sort on the same column of the new measure.
  teamsPanel.addEventListener('change', event => {
    if (event.target.form !== teamControls) return;
    const changes = {[event.target.name]: event.target.value};
    if (event.target.name === 'measure') changes['team-sort'] = teamControls.elements.namedItem('team-sort').value;
    update(changes);
  });
  quadrantLogoToggle.addEventListener('change', () => {
    if (!charts.teamQuadrant) return;
    charts.teamQuadrant.scatterShowLogos = quadrantLogoToggle.checked;
    charts.teamQuadrant.update('none');
  });
  root.querySelector('[data-team-quadrant-clear]').addEventListener('click', () => clearQuadrantSelection(charts.teamQuadrant));
  scatterLogoToggle.addEventListener('change', () => {
    if (!charts.teamScatter) return;
    charts.teamScatter.scatterShowLogos = scatterLogoToggle.checked;
    charts.teamScatter.update('none');
  });
  root.querySelector('[data-team-scatter-clear]').addEventListener('click', () => clearScatterSelection(charts.teamScatter));
  root.addEventListener('error', event => {
    if (event.target.matches('.team-logo, .explore-scatter-logo')) event.target.style.visibility = 'hidden';
  }, true);
  const updateTeamHistory = () => update({team: historyTeam.value, measure: historyMeasure.value, series: historySeries.value, context: historyContext.checked ? 'on' : 'off'});
  historyControls.addEventListener('submit', event => { event.preventDefault(); updateTeamHistory(); });
  // The chart card's controls sit outside the form but belong to it.
  historyPanel.addEventListener('change', event => { if (event.target.form === historyControls) updateTeamHistory(); });
  const updateSeasonTrend = () => {
    // An unchecked checkbox submits nothing, so clear opponents unless it does.
    const selection = {'trend-sort': null, 'trend-order': null, 'trend-mode': null, window: null, opponents: null, ...Object.fromEntries(new FormData(trendControls))};
    update(selection);
  };
  trendControls.addEventListener('submit', event => { event.preventDefault(); updateSeasonTrend(); });
  // The chart card's controls sit outside the form but belong to it.
  trendPanel.addEventListener('change', event => { if (event.target.form === trendControls) updateSeasonTrend(); });
  const updateRankings = () => update(Object.fromEntries(new FormData(rankingControls)));
  rankingControls.addEventListener('submit', event => { event.preventDefault(); updateRankings(); });
  rankingControls.addEventListener('change', updateRankings);
  if (paritySeasonSelect) {
    paritySeasonSelect.addEventListener('change', () => selectParitySeason(paritySeasonSelect.value));
    root.querySelector('[data-panel="parity"] [data-segmented="parity-series"]').addEventListener('change', () => update({view: 'parity', 'parity-series': paritySeriesControl.value}));
    root.querySelector('[data-parity-controls]').addEventListener('submit', event => {
      event.preventDefault(); update({view: 'parity', season: paritySeasonSelect.value, 'parity-series': paritySeriesControl.value});
    });
  }
  window.addEventListener('popstate', applyURL);
  styleTable();
  applyURL();
})();
