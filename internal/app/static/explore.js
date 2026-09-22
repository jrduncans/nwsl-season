(() => {
  const root = document.querySelector('[data-explore]');
  if (!root) return;
  const records = JSON.parse(root.querySelector('#explore-data').textContent) || [];
  const teamSeasons = JSON.parse(root.querySelector('#explore-team-data').textContent) || [];
  const historyContextData = JSON.parse(root.querySelector('#explore-context-data').textContent);
  const historySeries = root.querySelector('[data-history-series]');
  const historyContext = root.querySelector('[data-history-context]');
  const teamSeason = root.querySelector('[data-team-season]');
  const teamMeasure = root.querySelector('[data-team-measure]');
  const historyTeam = root.querySelector('[data-history-team]');
  const historyMeasure = root.querySelector('[data-history-measure]');
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
          if (chart.isDatasetVisible(datasetIndex) && value != null && (!dataset.keyboardOnce || index === 0)) marks.push({datasetIndex, index});
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
    return `${year}${row.active ? ' (in progress)' : ''}, ${series.label}: ${fixed(series.data[mark.index])} per match, ${row.played} played`;
  }
  function createTrend(canvas, rows = records, plugins = []) {
    const options = commonOptions();
    options.scales = {
      x: {grid: {display: false}, ticks: {maxRotation: 0, autoSkip: true, autoSkipPadding: 20, maxTicksLimit: 11}},
      y: {grace: '10%', grid: {color: color('--line')}, border: {display: false}, ticks: {maxTicksLimit: 6}},
    };
    options.plugins.tooltip.callbacks = {
      title: items => {
        const item = items[0];
        return item ? `${item.label}${item.chart.scoringRecords.get(item.label)?.active ? ' (in progress)' : ''}` : '';
      },
      label: item => `${item.dataset.label}: ${fixed(item.parsed.y)} per match`,
      afterLabel: item => `${item.chart.scoringRecords.get(item.label).played} played`,
    };
    const chart = new Chart(canvas, {
      type: 'line', options, plugins,
      data: {labels: [], datasets: [
        {label: 'Goals', key: 'goals', borderColor: goalsColor, backgroundColor: goalsColor, pointStyle: 'circle'},
        {label: 'xG', key: 'xg', borderColor: xgColor, backgroundColor: xgColor, pointStyle: 'rectRot'},
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
      for (const side of ['high', 'low']) {
        if (side === 'low' && series.low.value === series.high.value) continue;
        const bound = series[side], label = series.low.value === series.high.value ? 'high/low' : side;
        const text = `Record ${label} ${fixed(bound.value)}`;
        const y = chart.scales.y.getPixelForValue(bound.value) - 7;
        // Draw the full width even for a team with only one eligible season.
        ctx.strokeStyle = color('--muted'); ctx.lineWidth = 1.5; ctx.setLineDash([2, 5]);
        ctx.beginPath(); ctx.moveTo(area.left, y + 7); ctx.lineTo(area.right, y + 7); ctx.stroke();
        ctx.fillStyle = color('--paper'); ctx.fillRect(area.right - ctx.measureText(text).width - 7, y - 12, ctx.measureText(text).width + 8, 16);
        ctx.fillStyle = color('--muted'); ctx.fillText(text, area.right - 3, y);
      }
      ctx.restore();
    },
  };
  function historyContextDatasets(chart, series) {
    const seasons = new Map(series.seasons.map(row => [row.season, row]));
    const neutral = color('--muted');
    return ['low', 'high'].map(side => ({
      label: `Season ${side}`, contextKind: `season-${side}`,
      data: chart.data.labels.map(year => seasons.get(year)?.[side]?.value ?? null),
      borderColor: neutral, backgroundColor: '#dce2df80', borderWidth: 1, borderDash: [2, 3],
      pointBackgroundColor: neutral, pointRadius: 2.5, pointHitRadius: 6, pointHoverRadius: 5,
      fill: side === 'high' ? 2 : false, spanGaps: false, order: 2,
    })).concat(['low', 'high'].map(side => ({
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
    chart.options.scales.y = {...chart.options.scales.y, beginAtZero: true, min: domain.min, max: domain.max, title: {display: true, text: 'Per match'}};
    chart.options.plugins.tooltip.callbacks = {
      title: items => items[0] ? wrapHistoryTooltip(chart, [historyInspection(chart, {datasetIndex: items[0].datasetIndex, index: items[0].dataIndex}, true).title]) : [],
      label: item => wrapHistoryTooltip(chart, historyInspection(chart, {datasetIndex: item.datasetIndex, index: item.dataIndex}, true).lines),
    };
    canvas.setAttribute('aria-label', `${rows[0].name}: ${visible.map(key => key === 'goals' ? measure.actual : measure.expected).join(' and ')} per match, by regular season${context ? ', with league ranges and completed-season record holders' : ''}`);
    chart.resize(); chart.update('none');
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
    const visible = historySeries.value === 'both' ? ['goals', 'xg'] : [historySeries.value];
    const context = historyContextData[measure.index];
    root.querySelectorAll('[data-history-key]').forEach(key => { key.hidden = !visible.includes(key.dataset.historyKey); });
    root.querySelector('[data-history-context-details]').hidden = !historyContext.checked;
    root.querySelector('[data-history-context-legend]').hidden = !historyContext.checked;
    root.querySelector('[data-history-context-note]').hidden = !historyContext.checked;
    showHistoryContextDetails(visible.map(key => context[key]));
    const notes = visible.map(key => context[key].since ? `${context[key].label} records since ${context[key].since}` : `${context[key].label}: no completed-season records`);
    if (visible.includes('xg') && context.xg.partial) notes.push('Some season xG ranges are unavailable because team coverage is incomplete');
    root.querySelector('[data-history-context-note]').textContent = notes.join('. ') + '.';
    const rows = teamSeasons.flatMap(season => {
      const team = (season.teams || []).find(row => row.id === historyTeam.value);
      return team ? [{...team, season: season.season, active: season.active}] : [];
    });
    root.querySelector('[data-team-history-empty]').hidden = rows.length > 0;
    root.querySelector('[data-team-history-results]').hidden = rows.length === 0;
    root.querySelector('[data-team-history-warning]').hidden = !rows.some(row => row.values[0].expected === null);
    root.querySelector('[data-history-team-name]').replaceChildren(...(rows.length ? [teamName(rows[0])] : []));
    root.querySelector('[data-history-actual-label]').textContent = measure.actual;
    root.querySelector('[data-history-xg-label]').textContent = measure.expected;
    root.querySelector('[data-team-history-rows]').replaceChildren(...sortedTeamHistory(rows, params).map(row => {
      const tr = document.createElement('tr');
      const values = [row.season, row.played];
      [2, 0, 1].forEach(index => {
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
    const split = historyContext.checked && visible.length === 2;
    root.querySelector('[data-history-xg-panel]').hidden = !split;
    root.querySelector('[data-history-panel-label]').hidden = !split;
    root.querySelector('[data-history-panel-label]').textContent = measure.actual;
    root.querySelector('[data-history-xg-panel-label]').textContent = measure.expected;
    root.querySelector('[data-history-xg-empty]').hidden = !visible.includes('xg') || points.some(row => row.xg !== null);
    const values = points.flatMap(row => visible.map(key => row[key])).filter(value => value !== null);
    if (historyContext.checked) {
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
    showHistoryChart('team-history', points, rows, measure, split ? ['goals'] : visible, historyContext.checked ? context[visible[0]] : null, domain);
    if (split) showHistoryChart('team-history-xg', points, rows, measure, ['xg'], context.xg, domain);
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
  function applyURL() {
    const params = new URL(location.href).searchParams;
    const requested = params.get('view') || 'trend';
    const view = ['trend', 'distribution', 'table', 'teams', 'team-history'].includes(requested) ? requested : 'trend';
    dismiss();
    root.querySelectorAll('[data-panel]').forEach(panel => { panel.hidden = panel.dataset.panel !== view; });
    const teamView = view === 'teams' || view === 'team-history';
    root.querySelector('[data-league-views]').hidden = teamView;
    root.querySelector('[data-team-views]').hidden = !teamView;
    root.querySelectorAll('[data-team-display]').forEach(link => {
      const selected = view === 'teams' && link.dataset.teamDisplay === (params.get('display') === 'table' ? 'table' : 'chart');
      if (selected) link.setAttribute('aria-current', 'page'); else link.removeAttribute('aria-current');
      link.href = teamLink({display: link.dataset.teamDisplay});
    });
    root.querySelectorAll('[data-group-choice]').forEach(link => {
      if (link.dataset.groupChoice === (teamView ? 'teams' : 'league')) link.setAttribute('aria-current', 'page');
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
    if (canvas && !teamView && !charts[view]) charts[view] = view === 'trend' ? createTrend(canvas) : createDistribution(canvas);
    if (view === 'teams') showTeams(params);
    if (view === 'team-history') showTeamHistory(params);
    root.querySelectorAll('[data-view-choice], [data-group-choice]').forEach(link => {
      const target = link.dataset.viewChoice || (link.dataset.groupChoice === 'teams' ? 'teams' : 'trend');
      const selection = new URLSearchParams(params); selection.set('view', target);
      link.href = `?${selection}`;
    });
    if (view === 'trend' && charts.trend) {
      charts.trend.data.datasets.forEach((series, index) => charts.trend.setDatasetVisibility(index, metric.value === 'compare' || series.key === metric.value));
      charts.trend.update('none');
    }
    const column = Number(params.get('sort') || 0);
    sortTable(Number.isInteger(column) && column >= 0 && column <= 4 ? column : 0, params.get('order') === 'asc' ? 'asc' : 'desc');
    sortDistributionTable(params);
  }
  function update(changes) {
    const url = new URL(location.href);
    Object.entries(changes).forEach(([key, value]) => url.searchParams.set(key, value));
    history.pushState(null, '', url);
    applyURL();
  }
  root.addEventListener('click', event => {
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
      event.preventDefault(); update({view: link.dataset.viewChoice || (link.dataset.groupChoice === 'teams' ? 'teams' : 'trend')});
    }
    const sort = event.target.closest('[data-sort]');
    if (sort) update({sort: sort.dataset.sort, order: sort.parentElement.getAttribute('aria-sort') === 'descending' ? 'asc' : 'desc'});
  });
  document.addEventListener('pointerdown', event => { if (!event.target.closest('[data-chart]')) dismiss(); });
  document.addEventListener('keydown', event => { if (event.key === 'Escape') dismiss(); });
  metric.addEventListener('change', () => update({metric: metric.value}));
  root.querySelector('[data-team-controls]').addEventListener('submit', event => {
    event.preventDefault(); update({season: teamSeason.value, measure: teamMeasure.value});
  });
  teamSeason.addEventListener('change', () => update({season: teamSeason.value}));
  teamMeasure.addEventListener('change', () => update({measure: teamMeasure.value, 'team-sort': root.querySelector('[name="team-sort"]').value}));
  root.addEventListener('error', event => {
    if (event.target.matches('.team-logo')) event.target.style.visibility = 'hidden';
  }, true);
  root.querySelector('[data-team-history-controls]').addEventListener('submit', event => {
    event.preventDefault(); update({team: historyTeam.value, measure: historyMeasure.value, series: historySeries.value, context: historyContext.checked ? 'on' : 'off'});
  });
  historySeries.addEventListener('change', () => update({series: historySeries.value}));
  historyContext.addEventListener('change', () => update({context: historyContext.checked ? 'on' : 'off'}));
  historyTeam.addEventListener('change', () => update({team: historyTeam.value}));
  historyMeasure.addEventListener('change', () => update({measure: historyMeasure.value}));
  window.addEventListener('popstate', applyURL);
  styleTable();
  applyURL();
})();
