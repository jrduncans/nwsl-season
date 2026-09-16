(() => {
  const root = document.querySelector('[data-explore]');
  if (!root) return;
  const records = JSON.parse(root.querySelector('#explore-data').textContent) || [];
  const metric = root.querySelector('[data-metric]');
  const status = root.querySelector('[data-chart-status]');
  const charts = {};
  const styles = getComputedStyle(root);
  const color = name => styles.getPropertyValue(name).trim();
  const goalsColor = color('--green'), xgColor = color('--hypo');
  const binColors = ['#44504a', '#526d61', goalsColor, xgColor, '#372858'];
  const binLabels = ['0 goals', '1 goal', '2 goals', '3 goals', '4+ goals'];
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
    const body = root.querySelector('tbody');
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
    const requested = params.get('view') || root.dataset.view;
    const view = ['trend', 'distribution', 'table'].includes(requested) ? requested : 'trend';
    dismiss();
    root.querySelectorAll('[data-panel]').forEach(panel => { panel.hidden = panel.dataset.panel !== view; });
    root.querySelectorAll('[data-view-choice]').forEach(link => {
      if (link.dataset.viewChoice === view) link.setAttribute('aria-current', 'page');
      else link.removeAttribute('aria-current');
    });
    metric.value = ['goals', 'xg', 'compare'].includes(params.get('metric')) ? params.get('metric') : 'compare';
    root.querySelectorAll('[data-series]').forEach(series => {
      series.hidden = metric.value !== 'compare' && series.dataset.series !== metric.value;
    });
    const canvas = root.querySelector(`[data-chart="${view}"]`);
    if (canvas && !charts[view]) charts[view] = view === 'trend' ? createTrend(canvas) : createDistribution(canvas);
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
    const link = event.target.closest('[data-view-choice]');
    if (link && !event.ctrlKey && !event.metaKey && !event.shiftKey && !event.altKey) {
      event.preventDefault(); update({view: link.dataset.viewChoice});
    }
    const sort = event.target.closest('[data-sort]');
    if (sort) update({sort: sort.dataset.sort, order: sort.parentElement.getAttribute('aria-sort') === 'descending' ? 'asc' : 'desc'});
  });
  document.addEventListener('pointerdown', event => { if (!event.target.closest('[data-chart]')) dismiss(); });
  root.addEventListener('keydown', event => { if (event.key === 'Escape') dismiss(); });
  metric.addEventListener('change', () => update({metric: metric.value}));
  window.addEventListener('popstate', applyURL);
  styleTable();
  applyURL();
})();
