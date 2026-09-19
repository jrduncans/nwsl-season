(() => {
  const root = document.querySelector('[data-explore]');
  if (!root) return;
  const records = JSON.parse(root.querySelector('#explore-data').textContent) || [];
  const teamSeasons = JSON.parse(root.querySelector('#explore-team-data').textContent) || [];
  const teamSeason = root.querySelector('[data-team-season]');
  const teamMeasure = root.querySelector('[data-team-measure]');
  const metric = root.querySelector('[data-metric]');
  const status = root.querySelector('[data-chart-status]');
  const charts = {};
  const styles = getComputedStyle(root);
  const color = name => styles.getPropertyValue(name).trim();
  const goalsColor = color('--green'), xgColor = color('--hypo');
  const binColors = ['#44504a', '#526d61', goalsColor, xgColor, '#372858'];
  const binLabels = ['0 goals', '1 goal', '2 goals', '3 goals', '4+ goals'];
  const teamMeasures = {
    for: {index: 0, actual: 'Goals', expected: 'xG'},
    against: {index: 1, actual: 'Goals allowed', expected: 'xG allowed'},
    difference: {index: 2, actual: 'Goal differential', expected: 'xG differential'},
  };
  const fixed = value => (Math.abs(value) < .005 ? 0 : value).toFixed(2);
  const signed = value => `${value >= .005 ? '+' : ''}${fixed(value)}`;
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
  const clearChart = chart => {
    chart.setActiveElements([]);
    chart.tooltip.setActiveElements([], {x: 0, y: 0});
    chart.update('none');
  };
  function dismiss() {
    Object.values(charts).forEach(clearChart);
    status.textContent = '';
  }
  // Chart.js owns pointer/touch hit testing and tooltip placement. Its public
  // active-element API also lets keyboard users inspect the same marks.
  function keyboardAccess(chart, describe) {
    const canvas = chart.canvas;
    canvas.addEventListener('keydown', event => {
      if (event.key === 'Escape') { dismiss(); return; }
      if (!['ArrowLeft', 'ArrowRight', 'ArrowUp', 'ArrowDown', 'Home', 'End'].includes(event.key)) return;
      event.preventDefault();
      const marks = [];
      for (let index = 0; index < chart.data.labels.length; index++) {
        chart.data.datasets.forEach((dataset, datasetIndex) => {
          const value = dataset.data[index];
          if (chart.isDatasetVisible(datasetIndex) && value !== null) marks.push({datasetIndex, index});
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
      chart.setActiveElements([mark]);
      chart.tooltip.setActiveElements([mark], element.getCenterPoint());
      chart.update('none');
      status.textContent = describe(mark);
    });
    canvas.addEventListener('blur', () => { clearChart(chart); status.textContent = ''; });
  }
  function createTrend(canvas) {
    const first = Number(records[0].season), last = Number(records.at(-1).season);
    const years = Array.from({length: last - first + 1}, (_, i) => String(first + i));
    const byYear = new Map(records.map(row => [row.season, row]));
    const options = commonOptions();
    options.scales = {
      x: {grid: {display: false}, ticks: {maxRotation: 0, autoSkip: true, autoSkipPadding: 20, maxTicksLimit: 11}},
      y: {grace: '10%', grid: {color: color('--line')}, border: {display: false}, ticks: {maxTicksLimit: 6}},
    };
    options.plugins.tooltip.callbacks = {
      title: items => items[0]?.label || '',
      label: item => `${item.dataset.label}: ${item.parsed.y.toFixed(2)} per match`,
    };
    const chart = new Chart(canvas, {
      type: 'line', options,
      data: {labels: years, datasets: [
        {label: 'Goals', key: 'goals', borderColor: goalsColor, backgroundColor: goalsColor, pointStyle: 'circle'},
        {label: 'xG', key: 'xg', borderColor: xgColor, backgroundColor: xgColor, pointStyle: 'rectRot'},
      ].map(series => ({
        ...series, data: years.map(year => byYear.get(year)?.[series.key] ?? null),
        spanGaps: false, borderWidth: 2.5, pointRadius: 5, pointHitRadius: 4,
        pointHoverRadius: 8, pointHoverBorderWidth: 2, pointHoverBorderColor: color('--panel'),
      }))},
    });
    keyboardAccess(chart, mark => `${years[mark.index]}, ${chart.data.datasets[mark.datasetIndex].label}: ${chart.data.datasets[mark.datasetIndex].data[mark.index].toFixed(2)} per match`);
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
  function teamDescription(chart, index) {
    const row = chart.teamRows[index], measure = chart.teamMeasure;
    const value = row.values[measure.index];
    return [
      `${measure.actual}: ${fixed(value.actual)} per match`,
      `${measure.expected}: ${value.expected === null ? 'unavailable' : `${fixed(value.expected)} per match`}`,
      `Gap: ${value.expected === null ? 'unavailable' : `${signed(value.actual - value.expected)} per match`}`,
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
  function teamValue(row, measure, column) {
    const value = row.values[measure.index];
    if (column === 'name') return row.name;
    if (column === 'played') return row.played;
    if (column === 'gap') return value.expected === null ? null : value.actual - value.expected;
    return value[column];
  }
  function teamSortMetric(key) {
    for (const name of ['difference', 'for', 'against']) {
      if (key.startsWith(`${name}-`)) return {measure: teamMeasures[name], column: key.slice(name.length + 1)};
    }
    return {measure: teamMeasures.difference, column: key};
  }
  function sortedTeams(rows, measure, column, order) {
    return [...rows].sort((a, b) => {
      const x = teamValue(a, measure, column), y = teamValue(b, measure, column);
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
  function createTeams(canvas) {
    const options = commonOptions();
    options.indexAxis = 'y';
    options.scales = {
      x: {beginAtZero: true, position: 'top', grace: '10%', grid: {color: color('--line')}, border: {display: false}, ticks: {maxTicksLimit: 5}, title: {display: true, text: 'Per match'}},
      y: {offset: true, grid: {color: color('--line'), drawTicks: false}, border: {display: false}, ticks: {autoSkip: false, display: false}, afterFit: scale => {
        scale.width = root.querySelector('[data-team-labels]').offsetWidth;
      }},
    };
    options.plugins.tooltip.callbacks = {
      title: items => items[0] ? items[0].chart.teamRows[items[0].dataIndex].name : '',
      label: item => teamDescription(item.chart, item.dataIndex),
    };
    const chart = new Chart(canvas, {
      type: 'line', options,
      plugins: [{id: 'teamConnectors', afterLayout(chart) {
        root.querySelector('[data-team-labels]').childNodes.forEach((label, index) => {
          label.style.top = `${chart.scales.y.getPixelForValue(index)}px`;
        });
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
  function showTeams(params) {
    const season = teamSeasons.find(row => row.season === (params.get('season') || root.dataset.defaultTeamSeason));
    teamSeason.value = season?.season || '';
    teamMeasure.value = Object.hasOwn(teamMeasures, params.get('measure')) ? params.get('measure') : 'difference';
    const measure = teamMeasures[teamMeasure.value];
    const rows = season?.teams || [];
    const display = params.get('display') === 'table' ? 'table' : 'chart';
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
    root.querySelector('[data-team-measure-control]').hidden = display !== 'chart';
    root.querySelector('[data-team-plot]').hidden = display !== 'chart';
    root.querySelector('[data-team-table]').hidden = display !== 'table';
    root.querySelector('[data-team-warning]').hidden = !rows.some(row => row.values[measure.index].expected === null);
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
    body.replaceChildren(...sortedTeams(rows, tableSort.measure, tableSort.column, order).map(row => {
      const tr = document.createElement('tr');
      const values = [row.name, row.played];
      [2, 0, 1].forEach(index => {
        const value = row.values[index];
        values.push(fixed(value.actual), value.expected === null ? 'Unavailable' : fixed(value.expected),
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
    if (!rows.length || display !== 'chart') return;
    const chartRows = sortedTeams(rows, measure, 'actual', measure.index === 1 ? 'asc' : 'desc');
    const canvas = root.querySelector('[data-chart="teams"]');
    canvas.parentElement.style.height = `${chartRows.length * 48 + 70}px`;
    root.querySelector('[data-team-labels]').replaceChildren(...chartRows.map(teamName));
    const chart = charts.teams || (charts.teams = createTeams(canvas));
    chart.teamRows = chartRows; chart.teamMeasure = measure;
    chart.data.labels = chartRows.map(row => row.name);
    chart.data.datasets[0].data = chartRows.map(row => row.values[measure.index].actual);
    chart.data.datasets[1].data = chartRows.map(row => row.values[measure.index].expected);
    chart.data.datasets[0].label = measure.actual;
    chart.data.datasets[1].label = measure.expected;
    canvas.setAttribute('aria-label', `${season.season} regular season: ${measure.actual} and ${measure.expected} per team per match`);
    chart.resize(); chart.update('none');
  }
  function styleTable() {
    const cells = [...root.querySelectorAll('.explore-rate')];
    const maximum = Math.max(1, ...cells.map(cell => Number(cell.dataset.value)));
    cells.forEach(cell => {
      if (cell.dataset.value !== '') cell.style.setProperty('--rate-width', String(Number(cell.dataset.value) / maximum));
    });
    root.querySelectorAll('.explore-gap').forEach(cell => {
      if (cell.dataset.value === '') return;
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
  function applyURL() {
    const params = new URL(location.href).searchParams;
    const requested = params.get('view') || 'trend';
    const view = ['trend', 'distribution', 'table', 'teams'].includes(requested) ? requested : 'trend';
    dismiss();
    root.querySelectorAll('[data-panel]').forEach(panel => { panel.hidden = panel.dataset.panel !== view; });
    root.querySelector('[data-league-views]').hidden = view === 'teams';
    root.querySelectorAll('[data-group-choice]').forEach(link => {
      if (link.dataset.groupChoice === (view === 'teams' ? 'teams' : 'league')) link.setAttribute('aria-current', 'page');
      else link.removeAttribute('aria-current');
    });
    root.querySelectorAll('[data-view-choice]').forEach(link => {
      if (link.dataset.viewChoice === view) link.setAttribute('aria-current', 'page');
      else link.removeAttribute('aria-current');
    });
    metric.value = ['goals', 'xg', 'compare'].includes(params.get('metric')) ? params.get('metric') : 'compare';
    root.querySelectorAll('[data-series]').forEach(series => {
      series.hidden = metric.value !== 'compare' && series.dataset.series !== metric.value;
    });
    const canvas = root.querySelector(`[data-chart="${view}"]`);
    if (canvas && view !== 'teams' && !charts[view]) charts[view] = view === 'trend' ? createTrend(canvas) : createDistribution(canvas);
    if (view === 'teams') showTeams(params);
    if (view === 'trend' && charts.trend) {
      charts.trend.data.datasets.forEach((series, index) => charts.trend.setDatasetVisibility(index, metric.value === 'compare' || series.key === metric.value));
      charts.trend.update('none');
    }
    const column = Number(params.get('sort') || 0);
    sortTable(Number.isInteger(column) && column >= 0 && column <= 4 ? column : 0, params.get('order') === 'asc' ? 'asc' : 'desc');
  }
  function update(changes) {
    const url = new URL(location.href);
    Object.entries(changes).forEach(([key, value]) => url.searchParams.set(key, value));
    history.pushState(null, '', url);
    applyURL();
  }
  root.addEventListener('click', event => {
    const teamLink = event.target.closest('[data-team-display], [data-team-sort]');
    if (teamLink && !event.ctrlKey && !event.metaKey && !event.shiftKey && !event.altKey) {
      event.preventDefault();
      const params = new URL(teamLink.href).searchParams;
      const changes = {display: params.get('display')};
      if (teamLink.hasAttribute('data-team-sort')) {
        changes['team-sort'] = params.get('team-sort'); changes['team-order'] = params.get('team-order');
      }
      update(changes);
    }
    const link = event.target.closest('[data-view-choice], [data-group-choice]');
    if (link && !event.ctrlKey && !event.metaKey && !event.shiftKey && !event.altKey) {
      event.preventDefault(); update({view: link.dataset.viewChoice || (link.dataset.groupChoice === 'teams' ? 'teams' : 'trend')});
    }
    const sort = event.target.closest('[data-sort]');
    if (sort) update({sort: sort.dataset.sort, order: sort.parentElement.getAttribute('aria-sort') === 'descending' ? 'asc' : 'desc'});
  });
  document.addEventListener('pointerdown', event => { if (!event.target.closest('[data-chart]')) dismiss(); });
  root.addEventListener('keydown', event => { if (event.key === 'Escape') dismiss(); });
  metric.addEventListener('change', () => update({metric: metric.value}));
  root.querySelector('[data-team-controls]').addEventListener('submit', event => {
    event.preventDefault(); update({season: teamSeason.value, measure: teamMeasure.value});
  });
  teamSeason.addEventListener('change', () => update({season: teamSeason.value}));
  teamMeasure.addEventListener('change', () => update({measure: teamMeasure.value, 'team-sort': root.querySelector('[name="team-sort"]').value}));
  root.addEventListener('error', event => {
    if (event.target.matches('.team-logo')) event.target.style.visibility = 'hidden';
  }, true);
  window.addEventListener('popstate', applyURL);
  styleTable();
  applyURL();
})();
