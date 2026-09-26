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
  const teamUnits = root.querySelector('[data-team-units]');
  const historyTeam = root.querySelector('[data-history-team]');
  const historyMeasure = root.querySelector('[data-history-measure]');
  const metric = root.querySelector('[data-metric]');
  const distributionBin = root.querySelector('[data-distribution-bin]');
  const status = root.querySelector('[data-chart-status]');
  const scatterLogoToggle = root.querySelector('[data-team-scatter-logos]');
  const scatterSelection = root.querySelector('[data-team-scatter-selection]');
  const scatterSelectionTitle = root.querySelector('[data-team-scatter-selection-title]');
  const scatterSelectionTeams = root.querySelector('[data-team-scatter-selection-teams]');
  const scatterGuideValues = root.querySelector('[data-team-scatter-guide-values]');
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
    points: {index: 3, actual: 'Points', expected: 'xPts'},
  };
  const tableMeasures = [teamMeasures.difference, teamMeasures.for, teamMeasures.against, teamMeasures.points];
  const scatterMeaning = {
    for: 'Above: more goals scored than xG; below: fewer.',
    against: 'Above: more goals conceded than xG allowed; below: fewer.',
    difference: 'Above: higher goal differential than xG differential; below: lower.',
    points: 'Above: more points earned than xPts; below: fewer.',
  };
  const fixed = value => (Math.abs(value) < .005 ? 0 : value).toFixed(2);
  const signed = value => `${value >= .005 ? '+' : ''}${fixed(value)}`;
  const gapExtent = values => Math.max(.1, ...values.map(value => Math.abs(value))) * 1.15;
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
  function trendYAxis(gaps = null) {
    // The hidden bar dataset must not make the goals/xG line modes start at zero.
    const axis = {beginAtZero: false, grace: '10%', grid: {color: color('--line')}, border: {display: false}, ticks: {maxTicksLimit: 6}};
    if (gaps === null) return axis;
    const extent = gapExtent(gaps);
    return {...axis, grace: 0, min: -extent, max: extent,
      grid: {color: context => context.tick.value === 0 ? color('--muted') : color('--line')},
      // The symmetric limits are padding, not meaningful values to label.
      ticks: {maxTicksLimit: 7, includeBounds: false, callback: value => signed(value)},
      title: {display: true, text: 'Goals − xG per match'}};
  }
  const clearChart = chart => {
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
    if (clearPinned) clearScatterSelection(charts.teamScatter, false);
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
  function createLeagueTrend(canvas) {
    const chart = createTrend(canvas);
    chart.data.datasets.push({
      type: 'bar', label: 'Goals − xG', key: 'gap', hidden: true,
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
              ctx.fillText(gap === null ? (chart.teamMeasure.index === 3 ? 'xPts incomplete' : 'xG incomplete') : '0.00',
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
  function scatterDomain(rows, measure, units) {
    const values = rows.flatMap(row => {
      const metric = teamMetric(row, measure, units);
      return [metric.actual, metric.expected];
    });
    const low = Math.min(...values), high = Math.max(...values);
    const padding = Math.max((high - low) * .12, high === low ? Math.max(Math.abs(high) * .15, .15) : .06);
    return {min: measure.index === 2 ? low - padding : Math.max(0, low - padding), max: high + padding};
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
    const points = chart.getDatasetMeta(0).data.map((point, index) => {
      if (!point || point.skip) return null;
      const {x, y} = point.getProps(['x', 'y'], true);
      return {index, x, y};
    }).filter(Boolean);
    const size = 22, gap = 4, padding = 2;
    const {left, right, top, bottom} = chart.chartArea;
    const canvasBounds = chart.canvas.getBoundingClientRect();
    const layerBounds = layer.getBoundingClientRect();
    const offsetX = canvasBounds.left - layerBounds.left;
    const offsetY = canvasBounds.top - layerBounds.top;
    const visibleLabels = [];
    const overlaps = (first, second) =>
      first.left < second.right + padding && first.right + padding > second.left &&
      first.top < second.bottom + padding && first.bottom + padding > second.top;
    const obstructsPoint = (box, point) =>
      point.x >= box.left - 7 && point.x <= box.right + 7 &&
      point.y >= box.top - 7 && point.y <= box.bottom + 7;
    const candidates = points.filter(point => rows[point.index]?.logo).map(point => {
      const nearest = Math.min(Infinity, ...points
        .filter(other => other.index !== point.index)
        .map(other => Math.hypot(other.x - point.x, other.y - point.y)));
      return {...point, nearest};
    }).sort((a, b) => b.nearest - a.nearest || a.index - b.index);
    const placements = point => [
      {left: point.x - size / 2, top: point.y - size - gap},
      {left: point.x + gap, top: point.y - size / 2},
      {left: point.x - size / 2, top: point.y + gap},
      {left: point.x - size - gap, top: point.y - size / 2},
      {left: point.x + size / 2 - 2, top: point.y - size - gap},
      {left: point.x - size - size / 2 + 2, top: point.y - size - gap},
      {left: point.x + size / 2 - 2, top: point.y + gap},
      {left: point.x - size - size / 2 + 2, top: point.y + gap},
    ].map(position => ({...position, right: position.left + size, bottom: position.top + size}));
    for (const candidate of candidates) {
      const row = rows[candidate.index];
      const image = images.get(row.id);
      if (!image || (image.complete && !image.naturalWidth)) continue;
      const box = placements(candidate).find(rect => {
        if (rect.left < left || rect.right > right || rect.top < top || rect.bottom > bottom) return false;
        if (visibleLabels.some(label => overlaps(rect, label.rect))) return false;
        if (points.some(point => point.index !== candidate.index && obstructsPoint(rect, point))) return false;
        return true;
      });
      if (box) visibleLabels.push({index: candidate.index, rect: box, image});
    }
    for (const label of visibleLabels) {
      const {left, top} = label.rect;
      const {image} = label;
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
        pointRadius: 6, pointHitRadius: 6, pointHoverRadius: 9,
        pointHoverBorderColor: color('--gold'), pointHoverBorderWidth: 2,
      }]},
    });
    chart.scatterPinnedIndexes = [];
    chart.scatterGuideSource = null;
    chart.scatterShowLogos = scatterLogoToggle.checked;
    keyboardAccess(chart, mark => `${chart.teamRows[mark.index].name}. ${teamDescription(chart, mark.index).join('. ')}`);
    return chart;
  }
  function showTeams(params) {
    const season = teamSeasons.find(row => row.season === (params.get('season') || root.dataset.defaultTeamSeason));
    teamSeason.value = season?.season || '';
    teamMeasure.value = Object.hasOwn(teamMeasures, params.get('measure')) ? params.get('measure') : 'difference';
    teamUnits.value = params.get('units') === 'total' ? 'total' : 'per-match';
    const units = teamUnits.value;
    const unitLabel = units === 'total' ? 'in total' : 'per match';
    const measure = teamMeasures[teamMeasure.value];
    const rows = season?.teams || [];
    const display = ['chart', 'gap', 'scatter', 'table'].includes(params.get('display')) ? params.get('display') : 'chart';
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
      ? 'Actual − expected by team (regular-season)'
      : display === 'scatter' ? 'Actual vs expected by team (regular-season)' : 'Actual vs expected (regular-season)';
    root.querySelector('[data-team-measure-control]').hidden = display === 'table';
    root.querySelector('[data-team-units-note]').textContent =
      `${units === 'total' ? 'Totals' : 'Per match'}, from recorded results. Points compare earned points with ASA xPts from those matches.`;
    root.querySelector('[data-team-table-caption]').textContent =
      `${units === 'total' ? 'Totals' : 'Per-match values'}. Gap = actual − expected.`;
    root.querySelector('[data-team-plot]').hidden = display !== 'chart';
    root.querySelector('[data-team-gap-plot]').hidden = display !== 'gap';
    root.querySelector('[data-team-scatter-plot]').hidden = display !== 'scatter';
    root.querySelector('[data-team-table]').hidden = display !== 'table';
    const missingXG = rows.some(row => row.values[0].expected === null);
    const missingXPoints = rows.some(row => row.values[3].expected === null);
    const warningTypes = display === 'table'
      ? [missingXG && 'xG', missingXPoints && 'xPts'].filter(Boolean)
      : [measure.index === 3 ? missingXPoints && 'xPts' : missingXG && 'xG'].filter(Boolean);
    const warning = root.querySelector('[data-team-warning]');
    warning.hidden = warningTypes.length === 0;
    warning.textContent = warningTypes.length === 0 ? '' : warningTypes.length === 2
      ? 'Some match xG and xPts data is missing. Affected comparisons are unavailable.'
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
        ? `Bars show goals allowed minus xG allowed ${unitLabel}, from most negative to most positive. Negative means fewer goals conceded than expected.`
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
    const isPoints = measure.index === 3;
    root.querySelector('#explore-team-history-title').textContent =
      `${isPoints ? 'Points' : 'Scoring'} over time (regular-season)`;
    const choices = historySeries.options;
    choices[0].textContent = isPoints ? 'Points and xPts' : 'Goals and xG';
    choices[1].textContent = isPoints ? 'Points' : 'Goals';
    choices[2].textContent = isPoints ? 'xPts' : 'xG';
    root.querySelectorAll('[data-history-key]').forEach(key => { key.hidden = !visible.includes(key.dataset.historyKey); });
    root.querySelector('[data-history-context-details]').hidden = !contextEnabled;
    root.querySelector('[data-history-context-legend]').hidden = !contextEnabled;
    root.querySelector('[data-history-context-note]').hidden = !contextEnabled;
    showHistoryContextDetails(visible.map(key => context[key]));
    const notes = visible.map(key => context[key].since ? `${context[key].label} records since ${context[key].since}` : `${context[key].label}: no completed-season records`);
    if (visible.includes('xg') && context.xg.partial) notes.push(`Some season ${isPoints ? 'xPts' : 'xG'} ranges are unavailable because team coverage is incomplete`);
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
      ? 'Some seasons have incomplete xG and xPts for this team; actual values remain available.'
      : missingXPoints
        ? 'Some seasons have incomplete xPts for this team; earned points remain available.'
        : 'Some seasons have incomplete xG for this team; goals remain available.';
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
      const selected = view === 'teams' && link.dataset.teamDisplay ===
        (['chart', 'gap', 'scatter', 'table'].includes(params.get('display')) ? params.get('display') : 'chart');
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
    metric.value = ['goals', 'xg', 'compare', 'gap'].includes(params.get('metric')) ? params.get('metric') : 'compare';
    root.querySelector('#explore-trend-title').textContent = metric.value === 'gap'
      ? 'Goals − xG per match (regular-season)' : 'Goals and chances per match (regular-season)';
    root.querySelectorAll('[data-series]').forEach(series => {
      series.hidden = metric.value === 'gap' || (metric.value !== 'compare' && series.dataset.series !== metric.value);
    });
    if (distributionBin) {
      distributionBin.value = ['all', '0', '1', '2', '3', '4'].includes(params.get('distribution-bin'))
        ? params.get('distribution-bin') : 'all';
      root.querySelector('#explore-distribution-title').textContent = distributionBin.value === 'all'
        ? 'Share of regular-season matches by total goals'
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
    if (view === 'teams') showTeams(params);
    if (view === 'team-history') showTeamHistory(params);
    root.querySelectorAll('[data-view-choice], [data-group-choice]').forEach(link => {
      const target = link.dataset.viewChoice || (link.dataset.groupChoice === 'teams' ? 'teams' : 'trend');
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
        compare: 'Goals and expected goals per regular-season match, by season',
        goals: 'Goals per regular-season match, by season',
        xg: 'Expected goals per regular-season match, by season',
        gap: 'Goals minus expected goals per regular-season match, by season; positive bars are above xG and negative bars are below xG',
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
  document.addEventListener('pointerdown', event => { if (!event.target.closest('[data-chart]')) dismiss(false); });
  document.addEventListener('keydown', event => { if (event.key === 'Escape') dismiss(); });
  metric.addEventListener('change', () => update({metric: metric.value}));
  if (distributionBin) {
    distributionBin.addEventListener('change', () => update({'distribution-bin': distributionBin.value}));
    root.querySelector('[data-distribution-controls]').addEventListener('submit', event => {
      event.preventDefault(); update({'distribution-bin': distributionBin.value});
    });
  }
  root.querySelector('[data-team-controls]').addEventListener('submit', event => {
    event.preventDefault(); update({season: teamSeason.value, measure: teamMeasure.value, units: teamUnits.value});
  });
  teamSeason.addEventListener('change', () => update({season: teamSeason.value}));
  teamMeasure.addEventListener('change', () => update({measure: teamMeasure.value, 'team-sort': root.querySelector('[name="team-sort"]').value}));
  teamUnits.addEventListener('change', () => update({units: teamUnits.value}));
  scatterLogoToggle.addEventListener('change', () => {
    if (!charts.teamScatter) return;
    charts.teamScatter.scatterShowLogos = scatterLogoToggle.checked;
    charts.teamScatter.update('none');
  });
  root.querySelector('[data-team-scatter-clear]').addEventListener('click', () => clearScatterSelection(charts.teamScatter));
  root.addEventListener('error', event => {
    if (event.target.matches('.team-logo, .explore-scatter-logo')) event.target.style.visibility = 'hidden';
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
