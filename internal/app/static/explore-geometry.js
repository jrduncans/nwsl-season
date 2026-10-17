// Pure chart geometry for the Explore workspace. It takes numbers and returns
// numbers: no DOM, no Chart.js and no state, so the browser tests can call each
// function directly with edge cases. explore.js owns drawing and calls these.
(() => {
  // Half-width of the signed bar scales: the largest magnitude with a floor, so
  // an all-zero or tied population still has a visible range, plus 15% headroom.
  const gapExtent = values => Math.max(.1, ...values.map(value => Math.abs(value))) * 1.15;

  // Round line-chart axis for Match by match. Chart.js thins ticks on short
  // plots, which once left a ±4.6 axis labelled only at zero, so the ticks are
  // chosen here: a 1, 2, 2.5 or 5 step giving two or three ticks each side of
  // zero (two to four above zero when unsigned), ending on a tick at least 15%
  // beyond the largest value. All-integer data, such as per-match goals, keep
  // whole-number steps; decimals is how many places a step label needs.
  function trendAxis(values, signed = false) {
    const largest = signed ? Math.max(.1, ...values.map(Math.abs)) : Math.max(1, ...values);
    const target = largest * 1.15, limit = signed ? 3 : 4;
    const integer = values.length > 0 && values.every(Number.isInteger);
    let magnitude = 10 ** Math.floor(Math.log10(target / limit)), step;
    while (!step) {
      step = [1, 2, 2.5, 5].map(n => Number((n * magnitude).toPrecision(12)))
        .find(s => Math.ceil(target / s - 1e-9) <= limit && (!integer || Number.isInteger(s)));
      magnitude *= 10;
    }
    const count = Math.ceil(target / step - 1e-9), extent = Number((count * step).toPrecision(12));
    const ticks = Array.from({length: (signed ? 2 : 1) * count + 1}, (_, index) => Number(((index - (signed ? count : 0)) * step).toPrecision(12)));
    return {min: signed ? -extent : 0, max: extent, step, decimals: (String(step).split('.')[1] || '').length, ticks};
  }

  // Paired margins or side gaps share one symmetric rounded scale, including missing slots.
  const marginAxis = (goals, xg) => trendAxis([...goals, ...xg].filter(value => value !== null), true);

  // Each end keeps its own signed gap; missing goals or xG never become zero.
  const goalGap = (goals, xg, side) => goals[side] == null || xg[side] == null ? null : goals[side] - xg[side];

  // Fit the visible extremes with 5% padding on each side, rather than padding a
  // whole quadrant. A tied population still needs a small, nonzero range for
  // inspection. Nonnegative measures stop at zero; signed ones may go below it.
  function plotDomain(values, signed = false, roundBounds = false) {
    const low = Math.min(...values), high = Math.max(...values);
    const padding = high === low ? Math.max(Math.abs(high) * .025, .05) : (high - low) * .05;
    const min = signed ? low - padding : Math.max(0, low - padding), max = high + padding;
    if (!roundBounds) return {min, max};
    const unit = 10 ** Math.floor(Math.log10(max - min)) / 2;
    return {min: Math.floor(min / unit) * unit, max: Math.ceil(max / unit) * unit};
  }

  // Coincident screen positions form one inspection group. Nearby points stay
  // individually selectable; pointer and keyboard use the same membership.
  function scatterGroup(points, index) {
    const anchor = points.find(point => point.index === index);
    return anchor ? points.filter(point => Math.hypot(point.x - anchor.x, point.y - anchor.y) <= .25).map(point => point.index) : [];
  }

  // One radius for every point, defaulting to 6–10 CSS pixels (dense archive
  // plots use 3–6), limited by the nearest
  // pair of points and by the plot edges. Crowded or coincident points keep the
  // minimum. points are {x, y} in pixels; area is {left, right, top, bottom}.
  function pointRadius(points, area, minimum = 6, maximum = 10) {
    let clearance = Infinity;
    points.forEach((point, index) => {
      clearance = Math.min(clearance, point.x - area.left, area.right - point.x, point.y - area.top, area.bottom - point.y);
      points.slice(index + 1).forEach(other => {
        const distance = Math.hypot(other.x - point.x, other.y - point.y);
        // Crowded or coincident marks retain the existing minimum size.
        clearance = Math.min(clearance, (distance - 4) / 2);
      });
    });
    return Math.max(minimum, Math.min(maximum, Math.floor(clearance - 2)));
  }

  // The first of eight positions around point that holds a size x size logo
  // inside the plot area, clear of every placed logo (occupied boxes) and of
  // every point (obstacles, each with radius clearance). Undefined if none fits.
  function logoPlacement(point, size, area, obstacles, occupied, radius) {
    const gap = radius + 3, padding = 2;
    const placements = [
      {left: point.x - size / 2, top: point.y - size - gap},
      {left: point.x + gap, top: point.y - size / 2},
      {left: point.x - size / 2, top: point.y + gap},
      {left: point.x - size - gap, top: point.y - size / 2},
      {left: point.x + gap, top: point.y - size - gap},
      {left: point.x - size - gap, top: point.y - size - gap},
      {left: point.x + gap, top: point.y + gap},
      {left: point.x - size - gap, top: point.y + gap},
    ].map(position => ({...position, right: position.left + size, bottom: position.top + size}));
    return placements.find(rect => {
      if (rect.left < area.left || rect.right > area.right || rect.top < area.top || rect.bottom > area.bottom) return false;
      if (occupied.some(box => rect.left < box.right + padding && rect.right + padding > box.left && rect.top < box.bottom + padding && rect.bottom + padding > box.top)) return false;
      return !obstacles.some(other => other.x >= rect.left - radius - padding && other.x <= rect.right + radius + padding && other.y >= rect.top - radius - padding && other.y <= rect.bottom + radius + padding);
    });
  }

  // Places logos in two passes. First every logo gets a 22px box, most isolated
  // points first. Then each box grows to at most 48px (a twentieth of the plot
  // width, at least 22) without displacing the others. points are the labelled
  // series' marks as {x, y, index}; obstacles are every visible mark; usable says
  // whether a point has a logo that can be drawn. Returns [{point, rect}] in
  // placement order; points without room get no entry.
  function layoutLogos(points, obstacles, area, radius, usable) {
    const labels = [];
    const candidates = points.filter(usable).map(point => {
      const nearest = Math.min(Infinity, ...points
        .filter(other => other.index !== point.index)
        .map(other => Math.hypot(other.x - point.x, other.y - point.y)));
      return {...point, nearest};
    }).sort((a, b) => b.nearest - a.nearest || a.index - b.index);
    for (const candidate of candidates) {
      const rect = logoPlacement(candidate, 22, area, obstacles, labels.map(label => label.rect), radius);
      if (rect) labels.push({point: candidate, rect});
    }
    const maximumSize = Math.min(48, Math.max(22, Math.floor((area.right - area.left) / 20)));
    for (const label of labels) {
      const occupied = labels.filter(other => other !== label).map(other => other.rect);
      for (let size = maximumSize; size > 22; size -= 2) {
        const rect = logoPlacement(label.point, size, area, obstacles, occupied, radius);
        if (rect) { label.rect = rect; break; }
      }
    }
    return labels;
  }

  // Stacks horizontal spans into rows for a one-axis strip, so marks never
  // overlap. spans are {left, right} in pixels; each takes the lowest row whose
  // last span ends at least gap before it, visiting spans from left to right
  // (input order breaks ties). Returns each span's row, in input order.
  function stripRows(spans, gap = 0) {
    const rows = new Array(spans.length), ends = [];
    spans.map((span, index) => ({...span, index}))
      .sort((a, b) => a.left - b.left || a.index - b.index)
      .forEach(span => {
        let row = ends.findIndex(end => end + gap <= span.left);
        if (row === -1) row = ends.length;
        ends[row] = span.right;
        rows[span.index] = row;
      });
    return rows;
  }

  // One numeric scale for signed/unsigned ranked bars and companion markers.
  // Both series participate in the domain, so a marker can never be clipped.
  function rankingDomain(values, signed, points = false) {
    const extent = Math.max(.5, ...values.map(value => Math.abs(value) * 1.05));
    const unit = 10 ** Math.floor(Math.log10(extent));
    const rounded = points ? 3 : Number(([1, 2, 2.5, 3, 4, 5, 6, 8, 10].find(step => step * unit >= extent) * unit).toPrecision(12));
    return {min: signed ? -rounded : 0, max: rounded};
  }
  function rankingBar(value, companion, domain) {
    const position = v => (v - domain.min) / (domain.max - domain.min) * 100;
    const zero = position(0), end = position(value);
    return {zero, left: Math.min(zero, end), width: Math.abs(end - zero), marker: companion === null ? null : position(companion)};
  }

  // One meeting per slot, with a symmetric integer goal axis. No server copy:
  // without JavaScript the chart is a chronological, accessible results list.
  function headToHeadGeometry(margins, width, height = 260, series = 'actual') {
    const largest = Math.max(1, ...margins.filter(v => v !== null).map(Math.abs));
    const magnitude = 10 ** Math.floor(Math.log10(largest));
    // Both points and xPoints lie in [0, 3], so their difference lies in [−3, 3].
    const extent = series === 'points' ? 3 : [1, 2, 5, 10].map(n => n * magnitude).find(n => n >= largest);
    const left = 34, right = Math.max(left, width - 6), top = 20, bottom = height - 44;
    const zero = (top + bottom) / 2, scale = (bottom - top) / (2 * extent);
    const slot = (right - left) / Math.max(1, margins.length);
    const columns = margins.map((margin, index) => ({
      x: left + slot * (index + .15), width: slot * .7,
      y: margin > 0 ? zero - margin * scale : zero,
      height: Math.abs(margin) * scale,
      center: left + slot * (index + .5),
    }));
    return {extent, left, right, top, bottom, zero, columns};
  }
  // Missing values retain their meeting slot. Each view requires only its
  // own inputs: goals, xG, or both for the comparison.
  function headToHeadValue(goals, xg, series, points = null, xpoints = null) {
    if (series === 'points') return points === null || xpoints === null ? null : points - xpoints;
    const expected = xg.some(v => v === null) ? null : xg[0] - xg[1];
    if (series === 'expected') return expected;
    if (goals.some(v => v === null)) return null;
    const margin = goals[0] - goals[1];
    if (series === 'actual') return margin;
    return expected === null ? null : margin - expected;
  }
  // Nearest column across the full plot height; ties choose the earlier meeting.
  function headToHeadHit(columns, area, point) {
    if (point.x < area.left || point.x > area.right || point.y < area.top || point.y > area.bottom || !columns.length) return null;
    let nearest = 0;
    columns.forEach((column, index) => { if (Math.abs(column.center - point.x) < Math.abs(columns[nearest].center - point.x)) nearest = index; });
    return nearest;
  }
  // Labels stay at the first meeting of each season. Skip labels that cannot
  // fit, rather than shifting them to a different meeting. Widths are measured
  // by the caller in its actual font; no browser or Go geometry copy.
  function headToHeadLabels(candidates, area, gap = 8) {
    if (!candidates.length) return [];
    // Edge years define the scope. Move them inside the plot and reserve their
    // space before admitting intermediate years under their first meeting.
    const fit = label => ({...label, center: Math.max(area.left + label.width / 2, Math.min(area.right - label.width / 2, label.center))});
    const first = fit(candidates[0]);
    if (candidates.length === 1) return [first];
    const last = fit(candidates.at(-1));
    // A one-meeting first season can put both edge years in adjacent slots.
    // Keep both readable even then, moving their labels apart inside the plot.
    first.center = Math.min(first.center, area.right - last.width - gap - first.width / 2);
    last.center = Math.max(last.center, first.center + first.width / 2 + gap + last.width / 2);
    let end = first.center + first.width / 2;
    const limit = last.center - last.width / 2 - gap, labels = [first];
    for (const label of candidates.slice(1, -1)) {
      const left = label.center - label.width / 2, right = label.center + label.width / 2;
      if (left >= end + gap && right <= limit) { labels.push(label); end = right; }
    }
    return [...labels, last];
  }
  window.NWSLGeometry = Object.freeze({goalGap, marginAxis, headToHeadValue, headToHeadHit, headToHeadLabels, headToHeadGeometry, rankingDomain, rankingBar, gapExtent, trendAxis, plotDomain, scatterGroup, pointRadius, logoPlacement, layoutLogos, stripRows});
})();
