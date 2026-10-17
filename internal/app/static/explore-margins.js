// Numeric venue summaries and formatting for Margins and Goals vs xG.
// Both renderers consume the common match values and explicit goal coverage.
(() => {
  const measure = Object.freeze({scored: 0, allowed: 1, margin: 2, points: 3});
  const goals = row => row.goalsCovered ? [row.values[measure.scored].actual, row.values[measure.allowed].actual] : [null, null];
  const xg = row => [row.values[measure.scored].expected, row.values[measure.allowed].expected];
  const margin = values => values.some(value => value === null) ? null : values[0] - values[1];
  const goalMargin = row => margin(goals(row)), xgMargin = row => margin(xg(row));
  function summarize(matches) {
    return ['All', 'Home', 'Away'].map(label => {
      const tile = {label, played: 0, wins: 0, draws: 0, losses: 0, points: 0, goalsCovered: 0, xgCovered: 0, goalsFor: 0, goalsAgainst: 0, xgFor: 0, xgAgainst: 0};
      matches.filter(row => label === 'All' || row.venue === label).forEach(row => {
        tile.played++;
        const points = row.values[measure.points].actual;
        tile.points += points;
        tile[points === 3 ? 'wins' : points === 1 ? 'draws' : 'losses']++;
        if (row.goalsCovered) {
          tile.goalsCovered++; const [scored, allowed] = goals(row);
          tile.goalsFor += scored; tile.goalsAgainst += allowed;
        }
        const [scored, allowed] = xg(row);
        if (scored !== null && allowed !== null) {
          tile.xgCovered++; tile.xgFor += scored; tile.xgAgainst += allowed;
        }
      });
      return tile;
    });
  }
  function format(tile, {nonPenalty, xgLabel}) {
    const fixed = value => value.toFixed(2), n = tile.played;
    return {label: tile.label,
      record: `${tile.wins}–${tile.draws}–${tile.losses}`,
      recordLabel: `${tile.wins} wins, ${tile.draws} draws, ${tile.losses} losses`,
      rate: `${n ? fixed(tile.points / n) : '—'} pts/match`,
      goals: nonPenalty && !tile.goalsCovered ? 'Goals unavailable' : `${tile.goalsFor}–${tile.goalsAgainst} ${nonPenalty ? 'np goals' : 'goals'}`,
      coverage: nonPenalty && tile.goalsCovered < n ? `${tile.goalsCovered}/${n} with split` : '',
      xg: !n ? 'No matches' : tile.xgCovered === n ? `${fixed(tile.xgFor)}–${fixed(tile.xgAgainst)} ${xgLabel}` : `${xgLabel} incomplete`,
      xgCoverage: !n || tile.xgCovered < n ? `${xgLabel} ${tile.xgCovered}/${n} matches` : '',
      matches: `${n} ${n === 1 ? 'match' : 'matches'}`};
  }
  function formatGoalGap(tile, {nonPenalty, xgLabel}, unit) {
    const noun = unit === 'match' ? 'matches' : 'meetings', n = tile.played;
    const lines = [tile.goalsFor, tile.goalsAgainst].map((actual, side) => {
      if (!n) return `No ${noun}`;
      if (tile.goalsCovered !== n) return nonPenalty ? 'Non-penalty goals incomplete' : 'Goals incomplete';
      const label = `${nonPenalty ? 'non-penalty ' : ''}${side ? 'allowed' : 'goals'}`;
      if (tile.xgCovered !== n) return `${actual} ${label} · ${xgLabel} incomplete`;
      const expected = side ? tile.xgAgainst : tile.xgFor, gap = actual - expected;
      return `${actual} ${label} vs ${expected.toFixed(2)} ${xgLabel}${side ? ' allowed' : ''} · ${Math.abs(gap).toFixed(2)} ${gap < 0 ? 'below' : 'above'}`;
    });
    return {label: tile.label, scored: lines[0], allowed: lines[1],
      coverage: `${tile.xgCovered} of ${n} ${noun} with ${xgLabel}`,
      splitCoverage: nonPenalty && tile.goalsCovered < n ? `${tile.goalsCovered} of ${n} ${noun} with penalty split` : ''};
  }
  window.NWSLMargins = Object.freeze({measure, goals, xg, goalMargin, xgMargin, summarize, format, formatGoalGap});
})();
