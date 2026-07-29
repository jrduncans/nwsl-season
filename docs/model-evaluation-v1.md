# Model evaluation v1

Status: **complete**. Selected model: **xg-poisson-home-two-seasons-v1**.

Generated: 2026-07-29T00:18:35Z. Simulations: 20,000 iterations per cutoff; 10,000 paired bootstrap resamples.

Git commit: `6ef7f45b313561c62f859826d7b2b041e6bde659`.

## Data audit

| Season | Window | Included | Completed | xG coverage | Note |
| --- | --- | ---: | ---: | ---: | --- |
| 2016 | held_out | true | 100 | 100.0% | — |
| 2017 | development | true | 120 | 100.0% | — |
| 2018 | held_out | true | 108 | 100.0% | — |
| 2019 | development | true | 108 | 100.0% | — |
| 2021 | held_out | true | 118 | 100.0% | — |
| 2022 | development | true | 132 | 100.0% | — |
| 2023 | held_out | true | 132 | 100.0% | — |
| 2024 | development | true | 182 | 100.0% | — |
| 2025 | held_out | true | 182 | 100.0% | — |

## Evaluation protocol

Development: 2017, 2019, 2022, 2024. These seasons may guide new candidate model versions and their fixed constants.

Final test: 2016, 2018, 2021, 2023, 2025. These seasons are held out from model design and alone determine the recommendation.

Pooled results combine both windows for descriptive context only; they never determine the recommendation.

A formula, prior, or weight changed after inspecting the final-test results is a new model version and must wait for new untouched seasons before it can claim a final-test result.

## Summary results

Lower is better for every metric.

### Development results

| Model | Match log loss | Playoff Brier | Shield Brier | Points MAE | Points CRPS | Position MAE | Position RPS |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| Straight-line pace (`straight-line-pace-v1`) | 1.0409 | 0.1274 | 0.0571 | 5.023 | 3.608 | 1.589 | 0.1033 |
| Current pace (`current-pace-v1`) | 1.0365 | 0.1251 | 0.0582 | 5.031 | 3.623 | 1.580 | 0.1026 |
| Results Poisson (`results-poisson-home-two-seasons-v1`) | 1.0230 | 0.1233 | 0.0586 | 4.752 | 3.413 | 1.473 | 0.0942 |
| xG Poisson (`xg-poisson-home-two-seasons-v1`) | 1.0121 | 0.1112 | 0.0497 | 4.785 | 3.409 | 1.455 | 0.0907 |
| xG Poisson (recent form) (`xg-poisson-recent-form-v1`) | 1.0169 | 0.1099 | 0.0509 | 4.841 | 3.461 | 1.463 | 0.0913 |

### Final-test results

| Model | Match log loss | Playoff Brier | Shield Brier | Points MAE | Points CRPS | Position MAE | Position RPS |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| Straight-line pace (`straight-line-pace-v1`) | 1.0668 | 0.1215 | 0.0454 | 4.116 | 2.957 | 1.579 | 0.1060 |
| Current pace (`current-pace-v1`) | 1.0624 | 0.1210 | 0.0445 | 4.105 | 2.954 | 1.578 | 0.1058 |
| Results Poisson (`results-poisson-home-two-seasons-v1`) | 1.0582 | 0.1239 | 0.0456 | 4.287 | 3.076 | 1.608 | 0.1088 |
| xG Poisson (`xg-poisson-home-two-seasons-v1`) | 1.0478 | 0.1177 | 0.0421 | 3.998 | 2.842 | 1.530 | 0.1010 |
| xG Poisson (recent form) (`xg-poisson-recent-form-v1`) | 1.0499 | 0.1191 | 0.0417 | 4.027 | 2.864 | 1.545 | 0.1013 |

### Pooled results (descriptive only)

| Model | Match log loss | Playoff Brier | Shield Brier | Points MAE | Points CRPS | Position MAE | Position RPS |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| Straight-line pace (`straight-line-pace-v1`) | 1.0549 | 0.1243 | 0.0509 | 4.540 | 3.261 | 1.583 | 0.1048 |
| Current pace (`current-pace-v1`) | 1.0505 | 0.1229 | 0.0509 | 4.538 | 3.267 | 1.579 | 0.1043 |
| Results Poisson (`results-poisson-home-two-seasons-v1`) | 1.0421 | 0.1236 | 0.0517 | 4.504 | 3.233 | 1.545 | 0.1020 |
| xG Poisson (`xg-poisson-home-two-seasons-v1`) | 1.0314 | 0.1147 | 0.0457 | 4.366 | 3.107 | 1.495 | 0.0962 |
| xG Poisson (recent form) (`xg-poisson-recent-form-v1`) | 1.0348 | 0.1148 | 0.0460 | 4.408 | 3.143 | 1.507 | 0.0967 |

## Paired final-test comparisons

Differences are candidate minus incumbent; negative values favor the candidate.

| Candidate | Metric | Difference | 95% interval | Date blocks |
| --- | --- | ---: | ---: | ---: |
| `straight-line-pace-v1` | match_log_loss | +0.0132 | [-0.0071, +0.0333] | 296 |
| `straight-line-pace-v1` | playoff_brier | +0.0034 | [+0.0008, +0.0059] | 296 |
| `straight-line-pace-v1` | shield_brier | +0.0037 | [+0.0018, +0.0057] | 296 |
| `straight-line-pace-v1` | points_crps | +0.1023 | [+0.0636, +0.1422] | 296 |
| `straight-line-pace-v1` | position_rps | +0.0054 | [+0.0040, +0.0069] | 296 |
| `current-pace-v1` | match_log_loss | +0.0133 | [-0.0030, +0.0296] | 296 |
| `current-pace-v1` | playoff_brier | +0.0028 | [+0.0003, +0.0055] | 296 |
| `current-pace-v1` | shield_brier | +0.0027 | [+0.0006, +0.0049] | 296 |
| `current-pace-v1` | points_crps | +0.0989 | [+0.0613, +0.1375] | 296 |
| `current-pace-v1` | position_rps | +0.0052 | [+0.0039, +0.0066] | 296 |
| `results-poisson-home-two-seasons-v1` | match_log_loss | +0.0160 | [-0.0014, +0.0340] | 296 |
| `results-poisson-home-two-seasons-v1` | playoff_brier | +0.0061 | [+0.0033, +0.0091] | 296 |
| `results-poisson-home-two-seasons-v1` | shield_brier | +0.0034 | [+0.0018, +0.0051] | 296 |
| `results-poisson-home-two-seasons-v1` | points_crps | +0.2222 | [+0.1753, +0.2699] | 296 |
| `results-poisson-home-two-seasons-v1` | position_rps | +0.0081 | [+0.0067, +0.0097] | 296 |
| `xg-poisson-recent-form-v1` | match_log_loss | +0.0014 | [-0.0036, +0.0064] | 296 |
| `xg-poisson-recent-form-v1` | playoff_brier | +0.0009 | [+0.0003, +0.0015] | 296 |
| `xg-poisson-recent-form-v1` | shield_brier | -0.0003 | [-0.0008, +0.0002] | 296 |
| `xg-poisson-recent-form-v1` | points_crps | +0.0223 | [+0.0092, +0.0356] | 296 |
| `xg-poisson-recent-form-v1` | position_rps | +0.0003 | [-0.0000, +0.0005] | 296 |

The JSON artifact is the machine-readable source for all development/final-test stage buckets and fixed-decile calibration tables.

## Selection

No candidate met the precommitted replacement rule, so `xg-poisson-home-two-seasons-v1` remains selected.

- `straight-line-pace-v1` did not qualify: evaluation-only reference model; excluded from selection.
- `current-pace-v1` did not qualify: final-test log-loss bootstrap interval was not entirely below zero; Shield Brier guardrail failed.
- `results-poisson-home-two-seasons-v1` did not qualify: final-test log-loss bootstrap interval was not entirely below zero; playoff Brier guardrail failed; Shield Brier guardrail failed.
- `xg-poisson-recent-form-v1` did not qualify: final-test log-loss bootstrap interval was not entirely below zero.

## Limitations

- Historical ASA xG contains currently published or corrected values, not a reconstruction of when each value was first available.
- Daily UTC cutoffs prevent games on the same date from training one another.
