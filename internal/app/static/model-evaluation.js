(() => {
  const panel = document.querySelector('[data-evaluation-chart]');
  if (!panel || typeof Chart === 'undefined') return;
  let data;
  try {
    data = JSON.parse(panel.dataset.evaluation);
  } catch {
    return;
  }
  const metricControl = panel.querySelector('[data-evaluation-metric]');
  const scaleControl = panel.querySelector('[data-evaluation-scale]');
  const windowControl = panel.querySelector('[data-evaluation-window]');
  const canvas = panel.querySelector('[data-evaluation-canvas]');
  const summary = panel.querySelector('[data-evaluation-summary]');
  const legend = panel.querySelector('[data-evaluation-legend]');
  const status = panel.querySelector('[data-evaluation-status]');
  const baseline = data.models.find(model => model.id === data.baseline_id);
  if (!metricControl || !scaleControl || !windowControl || !canvas || !summary || !legend || !status || !baseline) {
    panel.replaceChildren(Object.assign(document.createElement('p'), {className: 'data-warning', textContent: 'The simple baseline is not available in this evaluation artifact.'}));
    return;
  }

  const styles = getComputedStyle(document.documentElement);
  const color = name => styles.getPropertyValue(name).trim();
  // Each model keeps its colour and point shape across every view, so a line
  // is recognisable after switching measure or seasons. The default model uses
  // the site green; the others use the Okabe–Ito colour-blind-safe palette.
  const known = {
    'straight-line-pace-v1': {ink: color('--muted'), shape: 'line'},
    'current-pace-v1': {ink: '#e69f00', shape: 'triangle'},
    'results-poisson-home-two-seasons-v1': {ink: '#56b4e9', shape: 'rect'},
    'xg-poisson-home-two-seasons-v1': {ink: '#0072b2', shape: 'rectRot'},
    'xg-poisson-recent-form-v1': {ink: '#cc79a7', shape: 'star'},
    'xg-poisson-schedule-load-v1': {ink: color('--green'), shape: 'circle'},
    'xg-poisson-npxg-strength-v1': {ink: '#d55e00', shape: 'crossRot'},
  };
  const spareInks = ['#7c3aed', '#a16207', '#be185d', '#0f766e', '#4b5563'];
  const spareShapes = ['circle', 'triangle', 'rect', 'rectRot', 'star'];
  const used = new Set(data.models.map(model => known[model.id]?.ink).filter(Boolean));
  const appearance = new Map();
  data.models.forEach((model, index) => {
    const ink = known[model.id]?.ink ?? spareInks.find(value => !used.has(value)) ?? spareInks[index % spareInks.length];
    used.add(ink);
    appearance.set(model.id, {ink, shape: known[model.id]?.shape ?? spareShapes[index % spareShapes.length]});
  });
  // A default ID absent from the artifact just leaves every line equal.
  const isDefault = model => model.id === data.default_id;
  const isBaseline = model => model.id === data.baseline_id;
  const label = model => isDefault(model) ? `${model.name} (default)` : model.name;
  const stageLabel = value => value.replace('-', '–');
  const hidden = new Set();

  const ratio = value => `${value.toFixed(2)}×`;
  const amount = (value, unit) => `${value.toFixed(2)} ${unit}`;

  function series() {
    const metric = metricControl.value;
    const field = metric === 'points' ? 'points_mae' : 'position_mae';
    const windowName = windowControl.value;
    const stages = baseline.windows[windowName]?.stages ?? [];
    const labels = stages.map(stage => stage.label);
    const base = new Map(stages.map(stage => [stage.label, stage[field]]));
    const models = data.models.map(model => {
      const byLabel = new Map((model.windows[windowName]?.stages ?? []).map(stage => [stage.label, stage[field]]));
      const errors = labels.map(key => byLabel.get(key) ?? null);
      const ratios = errors.map((value, index) => value == null || !base.get(labels[index]) ? null : value / base.get(labels[index]));
      return {model, errors, ratios};
    }).filter(entry => entry.errors.some(value => value != null));
    return {metric, labels, models, unit: metric === 'points' ? 'points' : 'places'};
  }

  // Every model at one stage, best first, for the tooltip and keyboard status.
  function inspection(view, index) {
    const relative = scaleControl.value === 'relative';
    const rows = view.models
      .filter(entry => !hidden.has(entry.model.id) && entry.errors[index] != null)
      .sort((a, b) => a.errors[index] - b.errors[index])
      .map(entry => {
        const error = amount(entry.errors[index], view.unit);
        const value = entry.ratios[index] == null ? error : relative
          ? `${ratio(entry.ratios[index])} baseline (${error})`
          : `${error} (${ratio(entry.ratios[index])} baseline)`;
        return {entry, text: `${label(entry.model)}: ${value}`};
      });
    return {title: `${stageLabel(view.labels[index])} of the regular season played`, rows};
  }

  const guide = {id: 'evaluationStageGuide', afterDraw(chart) {
    const mark = chart.getActiveElements()[0];
    if (!mark || !chart.chartArea) return;
    const x = chart.scales.x.getPixelForValue(mark.index);
    const {top, bottom} = chart.chartArea, ctx = chart.ctx;
    ctx.save(); ctx.strokeStyle = color('--muted'); ctx.lineWidth = 1;
    ctx.beginPath(); ctx.moveTo(x, top); ctx.lineTo(x, bottom); ctx.stroke(); ctx.restore();
  }};

  let view = series();
  const chart = new Chart(canvas, {
    type: 'line',
    data: {labels: [], datasets: []},
    plugins: [guide],
    options: {
      responsive: true, maintainAspectRatio: false, animation: false,
      color: color('--muted'), font: {family: 'system-ui, sans-serif', size: 13},
      interaction: {mode: 'index', axis: 'x', intersect: false},
      layout: {padding: {top: 8, right: 12}},
      scales: {
        x: {grid: {display: false}, title: {display: true, text: 'Share of the regular season played'}, ticks: {callback(value) { return stageLabel(this.getLabelForValue(value)); }}},
        y: {grace: '8%', grid: {color: context => scaleControl.value === 'relative' && Math.abs(context.tick.value - 1) < 1e-9 ? color('--muted') : color('--line')}, border: {display: false}, ticks: {maxTicksLimit: 7}, title: {display: true}},
      },
      plugins: {
        legend: {display: false},
        tooltip: {
          backgroundColor: color('--ink'), padding: 12, cornerRadius: 5,
          titleFont: {size: 13}, bodyFont: {size: 13}, boxPadding: 4, usePointStyle: true,
          // One tooltip row per model, ordered from the smallest error.
          itemSort: (a, b) => view.models[a.datasetIndex].errors[a.dataIndex] - view.models[b.datasetIndex].errors[b.dataIndex],
          callbacks: {
            title: items => items.length ? inspection(view, items[0].dataIndex).title : '',
            label: item => inspection(view, item.dataIndex).rows.find(row => row.entry === view.models[item.datasetIndex])?.text ?? '',
            labelColor: item => {
              const ink = appearance.get(view.models[item.datasetIndex].model.id).ink;
              return {borderColor: ink, backgroundColor: ink};
            },
            labelPointStyle: item => ({pointStyle: appearance.get(view.models[item.datasetIndex].model.id).shape, rotation: 0}),
          },
        },
      },
    },
  });

  function emphasise(id) {
    chart.data.datasets.forEach((dataset, index) => {
      const entry = view.models[index];
      const ink = appearance.get(entry.model.id).ink;
      const faded = id && entry.model.id !== id;
      dataset.borderColor = faded ? `${ink}33` : ink;
      dataset.pointBackgroundColor = faded ? `${ink}33` : ink;
      dataset.pointBorderColor = faded ? `${ink}33` : ink;
    });
    chart.update('none');
  }

  function renderLegend() {
    legend.replaceChildren(...view.models.map(entry => {
      const {model} = entry;
      const button = document.createElement('button');
      button.type = 'button';
      button.className = 'evaluation-legend-item';
      button.dataset.model = model.id;
      button.setAttribute('aria-pressed', String(!hidden.has(model.id)));
      button.title = hidden.has(model.id) ? `Show ${model.name}` : `Hide ${model.name}`;
      const marker = document.createElement('i');
      marker.style.background = appearance.get(model.id).ink;
      if (isBaseline(model)) marker.classList.add('evaluation-legend-dashed');
      if (isDefault(model)) marker.classList.add('evaluation-legend-default');
      button.append(marker, label(model));
      button.addEventListener('click', () => {
        if (hidden.has(model.id)) hidden.delete(model.id); else hidden.add(model.id);
        render();
      });
      button.addEventListener('mouseenter', () => emphasise(model.id));
      button.addEventListener('mouseleave', () => emphasise(null));
      button.addEventListener('focus', () => emphasise(model.id));
      button.addEventListener('blur', () => emphasise(null));
      return button;
    }));
  }

  function render() {
    view = series();
    const relative = scaleControl.value === 'relative';
    const measure = view.metric === 'points' ? 'Final-points forecast error' : 'Final table-position forecast error';
    chart.data.labels = view.labels;
    chart.data.datasets = view.models.map(entry => {
      const {model} = entry;
      const {ink, shape} = appearance.get(model.id);
      return {
        label: label(model),
        data: relative ? entry.ratios : entry.errors,
        borderColor: ink, backgroundColor: ink, pointBackgroundColor: ink, pointBorderColor: ink,
        borderWidth: isDefault(model) ? 3.5 : 2,
        borderDash: isBaseline(model) ? [7, 4] : [],
        pointStyle: shape === 'line' ? 'circle' : shape,
        pointRadius: isBaseline(model) ? 2.5 : 4, pointHoverRadius: 6,
        // Open marks such as crosses need a heavier stroke to read at size.
        pointBorderWidth: ['crossRot', 'star'].includes(shape) ? 2 : 1,
        tension: 0, spanGaps: false,
        hidden: hidden.has(model.id),
        order: isDefault(model) ? 0 : 1,
      };
    });
    chart.options.scales.y.title.text = relative ? 'Error relative to the simple baseline' : `Average error per team (${view.unit})`;
    chart.options.scales.y.ticks.callback = value => relative ? ratio(value) : value.toFixed(1);
    chart.options.scales.y.beginAtZero = !relative;
    canvas.setAttribute('aria-label', `${measure} through the season, ${windowControl.selectedOptions[0].textContent.toLowerCase()} seasons`);
    clear();
    chart.update('none');
    renderLegend();
    summary.textContent = relative
      ? 'The simple straight-line pace baseline is fixed at 1.00×. Lower lines are closer to the final table.'
      : `Lower is better: this is the average final-table error per team, measured in ${view.unit}.`;
  }

  function clear() {
    chart.setActiveElements([]);
    chart.tooltip.setActiveElements([], {x: 0, y: 0});
    status.textContent = '';
  }

  // Chart.js owns pointer and touch hit testing; the keyboard steps through
  // the same stages and announces every model at the chosen one.
  canvas.addEventListener('keydown', event => {
    if (event.key === 'Escape') { clear(); chart.update('none'); return; }
    if (!['ArrowLeft', 'ArrowRight', 'ArrowUp', 'ArrowDown', 'Home', 'End'].includes(event.key)) return;
    event.preventDefault();
    const last = view.labels.length - 1;
    if (last < 0) return;
    const current = chart.getActiveElements()[0]?.index;
    let index = current ?? (['ArrowLeft', 'ArrowUp'].includes(event.key) ? last + 1 : -1);
    if (event.key === 'Home') index = 0;
    else if (event.key === 'End') index = last;
    else index = Math.max(0, Math.min(last, index + (['ArrowLeft', 'ArrowUp'].includes(event.key) ? -1 : 1)));
    const marks = chart.data.datasets.flatMap((dataset, datasetIndex) =>
      chart.isDatasetVisible(datasetIndex) && dataset.data[index] != null ? [{datasetIndex, index}] : []);
    if (!marks.length) return;
    const point = chart.getDatasetMeta(marks[0].datasetIndex).data[index].getCenterPoint();
    chart.setActiveElements(marks);
    chart.tooltip.setActiveElements(marks, point);
    chart.update('none');
    const {title, rows} = inspection(view, index);
    status.textContent = `${title}. ${rows.map(row => row.text).join('. ')}`;
  });
  canvas.addEventListener('blur', () => { clear(); chart.update('none'); });

  [metricControl, scaleControl, windowControl].forEach(control => control.addEventListener('change', render));
  render();
})();
