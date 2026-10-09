// Pure chart geometry for the Explore workspace. It takes numbers and returns
// numbers: no DOM, no Chart.js and no state, so the browser tests can call each
// function directly with edge cases. explore.js owns drawing and calls these.
(() => {
  // Half-width of the signed bar scales: the largest magnitude with a floor, so
  // an all-zero or tied population still has a visible range, plus 15% headroom.
  const gapExtent = values => Math.max(.1, ...values.map(value => Math.abs(value))) * 1.15;

  // Fit the visible extremes with 5% padding on each side, rather than padding a
  // whole quadrant. A tied population still needs a small, nonzero range for
  // inspection. Nonnegative measures stop at zero; signed ones may go below it.
  function plotDomain(values, signed = false) {
    const low = Math.min(...values), high = Math.max(...values);
    const padding = high === low ? Math.max(Math.abs(high) * .025, .05) : (high - low) * .05;
    return {min: signed ? low - padding : Math.max(0, low - padding), max: high + padding};
  }

  // One radius for every point, from 6 to 10 CSS pixels, limited by the nearest
  // pair of points and by the plot edges. Crowded or coincident points keep the
  // minimum. points are {x, y} in pixels; area is {left, right, top, bottom}.
  function pointRadius(points, area) {
    let clearance = Infinity;
    points.forEach((point, index) => {
      clearance = Math.min(clearance, point.x - area.left, area.right - point.x, point.y - area.top, area.bottom - point.y);
      points.slice(index + 1).forEach(other => {
        const distance = Math.hypot(other.x - point.x, other.y - point.y);
        // Crowded or coincident marks retain the existing minimum size.
        clearance = Math.min(clearance, (distance - 4) / 2);
      });
    });
    return Math.max(6, Math.min(10, Math.floor(clearance - 2)));
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

  window.NWSLGeometry = Object.freeze({gapExtent, plotDomain, pointRadius, logoPlacement, layoutLogos});
})();
