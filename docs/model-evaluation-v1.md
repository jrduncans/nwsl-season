# Model evaluation v1

Status: **complete**. Selected model: **xg-poisson-v1**.

Generated: 2026-07-27T21:05:07Z. Simulations: 20,000 iterations per cutoff; 10,000 paired bootstrap resamples.

Git commit: `3ef9e38aa2d89fa0794a69be4b68596a96fae561`.

## Data audit

| Season | Window | Included | Completed | xG coverage | Note |
| --- | --- | ---: | ---: | ---: | --- |
| 2016 | development | true | 100 | 100.0% | — |
| 2017 | development | true | 120 | 100.0% | — |
| 2018 | development | true | 108 | 100.0% | — |
| 2019 | development | true | 108 | 100.0% | — |
| 2021 | development | true | 118 | 100.0% | — |
| 2022 | development | true | 132 | 100.0% | — |
| 2023 | held_out | true | 132 | 100.0% | — |
| 2024 | held_out | true | 182 | 100.0% | — |
| 2025 | held_out | true | 182 | 100.0% | — |

## Evaluation protocol

Development: 2016, 2017, 2018, 2019, 2021, 2022. These seasons may guide new candidate model versions and their fixed constants.

Final test: 2023, 2024, 2025. These seasons are held out from model design and alone determine the recommendation.

Pooled results combine both windows for descriptive context only; they never determine the recommendation.

A formula, prior, or weight changed after inspecting the final-test results is a new model version and must wait for new untouched seasons before it can claim a final-test result.

## Summary results

Lower is better for every metric.

### Development results

| Model | Match log loss | Playoff Brier | Shield Brier | Points MAE | Points CRPS | Position MAE | Position RPS |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| Current pace (`current-pace-v1`) | 1.0486 | 0.1317 | 0.0603 | 4.687 | 3.373 | 1.508 | 0.1125 |
| Results Poisson (`results-poisson-v1`) | 1.0382 | 0.1298 | 0.0589 | 4.475 | 3.219 | 1.427 | 0.1069 |
| xG Poisson (`xg-poisson-v1`) | 1.0337 | 0.1167 | 0.0501 | 4.456 | 3.154 | 1.378 | 0.0995 |

### Final-test results

| Model | Match log loss | Playoff Brier | Shield Brier | Points MAE | Points CRPS | Position MAE | Position RPS |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| Current pace (`current-pace-v1`) | 1.0532 | 0.1110 | 0.0380 | 4.334 | 3.120 | 1.676 | 0.0931 |
| Results Poisson (`results-poisson-v1`) | 1.0527 | 0.1144 | 0.0412 | 4.525 | 3.241 | 1.697 | 0.0946 |
| xG Poisson (`xg-poisson-v1`) | 1.0326 | 0.1116 | 0.0400 | 4.252 | 3.050 | 1.654 | 0.0915 |

### Pooled results (descriptive only)

| Model | Match log loss | Playoff Brier | Shield Brier | Points MAE | Points CRPS | Position MAE | Position RPS |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| Current pace (`current-pace-v1`) | 1.0505 | 0.1229 | 0.0509 | 4.538 | 3.267 | 1.579 | 0.1043 |
| Results Poisson (`results-poisson-v1`) | 1.0443 | 0.1233 | 0.0515 | 4.496 | 3.229 | 1.541 | 0.1017 |
| xG Poisson (`xg-poisson-v1`) | 1.0332 | 0.1145 | 0.0458 | 4.370 | 3.110 | 1.494 | 0.0961 |

## Paired final-test comparisons

Differences are candidate minus incumbent; negative values favor the candidate.

| Candidate | Metric | Difference | 95% interval | Date blocks |
| --- | --- | ---: | ---: | ---: |
| `current-pace-v1` | match_log_loss | -0.0084 | [-0.0215, +0.0046] | 194 |
| `current-pace-v1` | playoff_brier | -0.0034 | [-0.0052, -0.0016] | 194 |
| `current-pace-v1` | shield_brier | -0.0037 | [-0.0050, -0.0025] | 194 |
| `current-pace-v1` | points_crps | -0.1273 | [-0.1528, -0.1013] | 194 |
| `current-pace-v1` | position_rps | -0.0015 | [-0.0024, -0.0007] | 194 |
| `xg-poisson-v1` | match_log_loss | -0.0205 | [-0.0395, -0.0016] | 194 |
| `xg-poisson-v1` | playoff_brier | -0.0031 | [-0.0063, +0.0001] | 194 |
| `xg-poisson-v1` | shield_brier | -0.0011 | [-0.0030, +0.0007] | 194 |
| `xg-poisson-v1` | points_crps | -0.1980 | [-0.2418, -0.1560] | 194 |
| `xg-poisson-v1` | position_rps | -0.0032 | [-0.0043, -0.0021] | 194 |

The JSON artifact is the machine-readable source for all development/final-test stage buckets and fixed-decile calibration tables.

## Selection

`xg-poisson-v1` met the precommitted replacement rule and had the lowest qualifying final-test match log loss.

- `current-pace-v1` did not qualify: final-test log-loss bootstrap interval was not entirely below zero.
- `xg-poisson-v1` qualified: passed the precommitted bootstrap and guardrail rule.

## Limitations

- Historical ASA xG contains currently published or corrected values, not a reconstruction of when each value was first available.
- Daily UTC cutoffs prevent games on the same date from training one another.
