# Model evaluation v1

Status: **complete**. Selected model: **xg-poisson-schedule-load-v1**.

Generated: 2026-10-11T18:27:29Z. Simulations: 20,000 iterations per cutoff; 10,000 paired bootstrap resamples.

Git commit: `a5e7210f4fdc7df7a593fb74397461d050a31408`.

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
| xG Poisson (schedule load) (`xg-poisson-schedule-load-v1`) | 1.0119 | 0.1095 | 0.0505 | 4.772 | 3.399 | 1.449 | 0.0902 |
| xG Poisson (npxG strength, schedule load) (`xg-poisson-npxg-strength-v1`) | 1.0097 | 0.1076 | 0.0487 | 4.715 | 3.369 | 1.431 | 0.0892 |

### Final-test results

| Model | Match log loss | Playoff Brier | Shield Brier | Points MAE | Points CRPS | Position MAE | Position RPS |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| Straight-line pace (`straight-line-pace-v1`) | 1.0668 | 0.1215 | 0.0454 | 4.116 | 2.957 | 1.579 | 0.1060 |
| Current pace (`current-pace-v1`) | 1.0624 | 0.1210 | 0.0445 | 4.105 | 2.954 | 1.578 | 0.1058 |
| Results Poisson (`results-poisson-home-two-seasons-v1`) | 1.0582 | 0.1239 | 0.0456 | 4.287 | 3.076 | 1.608 | 0.1088 |
| xG Poisson (`xg-poisson-home-two-seasons-v1`) | 1.0478 | 0.1177 | 0.0421 | 3.998 | 2.842 | 1.530 | 0.1010 |
| xG Poisson (recent form) (`xg-poisson-recent-form-v1`) | 1.0499 | 0.1191 | 0.0417 | 4.027 | 2.864 | 1.545 | 0.1013 |
| xG Poisson (schedule load) (`xg-poisson-schedule-load-v1`) | 1.0467 | 0.1175 | 0.0419 | 3.994 | 2.838 | 1.528 | 0.1009 |
| xG Poisson (npxG strength, schedule load) (`xg-poisson-npxg-strength-v1`) | 1.0459 | 0.1168 | 0.0422 | 3.984 | 2.834 | 1.539 | 0.1015 |

### Pooled results (descriptive only)

| Model | Match log loss | Playoff Brier | Shield Brier | Points MAE | Points CRPS | Position MAE | Position RPS |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| Straight-line pace (`straight-line-pace-v1`) | 1.0549 | 0.1243 | 0.0509 | 4.540 | 3.261 | 1.583 | 0.1048 |
| Current pace (`current-pace-v1`) | 1.0505 | 0.1229 | 0.0509 | 4.538 | 3.267 | 1.579 | 0.1043 |
| Results Poisson (`results-poisson-home-two-seasons-v1`) | 1.0421 | 0.1236 | 0.0517 | 4.504 | 3.233 | 1.545 | 0.1020 |
| xG Poisson (`xg-poisson-home-two-seasons-v1`) | 1.0314 | 0.1147 | 0.0457 | 4.366 | 3.107 | 1.495 | 0.0962 |
| xG Poisson (recent form) (`xg-poisson-recent-form-v1`) | 1.0348 | 0.1148 | 0.0460 | 4.408 | 3.143 | 1.507 | 0.0967 |
| xG Poisson (schedule load) (`xg-poisson-schedule-load-v1`) | 1.0307 | 0.1138 | 0.0459 | 4.358 | 3.101 | 1.491 | 0.0959 |
| xG Poisson (npxG strength, schedule load) (`xg-poisson-npxg-strength-v1`) | 1.0293 | 0.1125 | 0.0452 | 4.326 | 3.084 | 1.489 | 0.0957 |

## Paired final-test comparisons

Differences are candidate minus incumbent; negative values favor the candidate.

| Candidate | Metric | Difference | 95% interval | Date blocks |
| --- | --- | ---: | ---: | ---: |
| `straight-line-pace-v1` | match_log_loss | +0.0151 | [-0.0055, +0.0355] | 296 |
| `straight-line-pace-v1` | playoff_brier | +0.0036 | [+0.0010, +0.0062] | 296 |
| `straight-line-pace-v1` | shield_brier | +0.0039 | [+0.0020, +0.0059] | 296 |
| `straight-line-pace-v1` | points_crps | +0.1066 | [+0.0683, +0.1463] | 296 |
| `straight-line-pace-v1` | position_rps | +0.0056 | [+0.0042, +0.0070] | 296 |
| `current-pace-v1` | match_log_loss | +0.0151 | [-0.0014, +0.0317] | 296 |
| `current-pace-v1` | playoff_brier | +0.0031 | [+0.0005, +0.0058] | 296 |
| `current-pace-v1` | shield_brier | +0.0029 | [+0.0009, +0.0051] | 296 |
| `current-pace-v1` | points_crps | +0.1032 | [+0.0658, +0.1418] | 296 |
| `current-pace-v1` | position_rps | +0.0053 | [+0.0040, +0.0067] | 296 |
| `results-poisson-home-two-seasons-v1` | match_log_loss | +0.0179 | [+0.0004, +0.0361] | 296 |
| `results-poisson-home-two-seasons-v1` | playoff_brier | +0.0064 | [+0.0035, +0.0094] | 296 |
| `results-poisson-home-two-seasons-v1` | shield_brier | +0.0036 | [+0.0020, +0.0053] | 296 |
| `results-poisson-home-two-seasons-v1` | points_crps | +0.2265 | [+0.1800, +0.2741] | 296 |
| `results-poisson-home-two-seasons-v1` | position_rps | +0.0083 | [+0.0068, +0.0098] | 296 |
| `xg-poisson-home-two-seasons-v1` | match_log_loss | +0.0019 | [+0.0004, +0.0035] | 296 |
| `xg-poisson-home-two-seasons-v1` | playoff_brier | +0.0002 | [+0.0001, +0.0004] | 296 |
| `xg-poisson-home-two-seasons-v1` | shield_brier | +0.0002 | [+0.0001, +0.0003] | 296 |
| `xg-poisson-home-two-seasons-v1` | points_crps | +0.0043 | [+0.0026, +0.0061] | 296 |
| `xg-poisson-home-two-seasons-v1` | position_rps | +0.0001 | [+0.0001, +0.0002] | 296 |
| `xg-poisson-recent-form-v1` | match_log_loss | +0.0033 | [-0.0020, +0.0084] | 296 |
| `xg-poisson-recent-form-v1` | playoff_brier | +0.0011 | [+0.0005, +0.0017] | 296 |
| `xg-poisson-recent-form-v1` | shield_brier | -0.0001 | [-0.0006, +0.0004] | 296 |
| `xg-poisson-recent-form-v1` | points_crps | +0.0266 | [+0.0136, +0.0399] | 296 |
| `xg-poisson-recent-form-v1` | position_rps | +0.0004 | [+0.0001, +0.0007] | 296 |
| `xg-poisson-npxg-strength-v1` | match_log_loss | -0.0018 | [-0.0061, +0.0025] | 296 |
| `xg-poisson-npxg-strength-v1` | playoff_brier | -0.0003 | [-0.0011, +0.0005] | 296 |
| `xg-poisson-npxg-strength-v1` | shield_brier | +0.0004 | [+0.0000, +0.0007] | 296 |
| `xg-poisson-npxg-strength-v1` | points_crps | -0.0016 | [-0.0155, +0.0124] | 296 |
| `xg-poisson-npxg-strength-v1` | position_rps | +0.0005 | [+0.0001, +0.0010] | 296 |

The JSON artifact is the machine-readable source for all development/final-test stage buckets and fixed-decile calibration tables.

## Selection

No candidate met the precommitted replacement rule, so `xg-poisson-schedule-load-v1` remains selected.

- `straight-line-pace-v1` did not qualify: evaluation-only reference model; excluded from selection.
- `current-pace-v1` did not qualify: final-test log-loss bootstrap interval was not entirely below zero; Shield Brier guardrail failed.
- `results-poisson-home-two-seasons-v1` did not qualify: final-test log-loss bootstrap interval was not entirely below zero; playoff Brier guardrail failed; Shield Brier guardrail failed.
- `xg-poisson-home-two-seasons-v1` did not qualify: final-test log-loss bootstrap interval was not entirely below zero.
- `xg-poisson-recent-form-v1` did not qualify: final-test log-loss bootstrap interval was not entirely below zero.
- `xg-poisson-npxg-strength-v1` did not qualify: final-test log-loss bootstrap interval was not entirely below zero.

## Limitations

- Historical ASA xG contains currently published or corrected values, not a reconstruction of when each value was first available.
- Historical kickoffs use the currently cached or corrected schedule, not an as-of reconstruction; a retrospective reschedule can reveal schedule knowledge that was unavailable at the cutoff.
- Daily UTC cutoffs prevent games on the same date from training one another.
