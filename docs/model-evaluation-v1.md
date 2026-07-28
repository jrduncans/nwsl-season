# Model evaluation v1

Status: **complete**. Selected model: **results-poisson-v1**.

Generated: 2026-07-27T21:05:07Z. Simulations: 20,000 iterations per cutoff; 10,000 paired bootstrap resamples.

Git commit: `ed081d72983724d191bb025d4d08a03a340330eb`.

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
| Results Poisson (`results-poisson-v1`) | 1.0251 | 0.1235 | 0.0582 | 4.757 | 3.417 | 1.471 | 0.0940 |
| xG Poisson (`xg-poisson-v1`) | 1.0133 | 0.1111 | 0.0500 | 4.791 | 3.413 | 1.456 | 0.0906 |

### Final-test results

| Model | Match log loss | Playoff Brier | Shield Brier | Points MAE | Points CRPS | Position MAE | Position RPS |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| Straight-line pace (`straight-line-pace-v1`) | 1.0668 | 0.1215 | 0.0454 | 4.116 | 2.957 | 1.579 | 0.1060 |
| Current pace (`current-pace-v1`) | 1.0624 | 0.1210 | 0.0445 | 4.105 | 2.954 | 1.578 | 0.1058 |
| Results Poisson (`results-poisson-v1`) | 1.0605 | 0.1232 | 0.0456 | 4.267 | 3.063 | 1.602 | 0.1085 |
| xG Poisson (`xg-poisson-v1`) | 1.0501 | 0.1175 | 0.0422 | 4.001 | 2.844 | 1.528 | 0.1010 |

### Pooled results (descriptive only)

| Model | Match log loss | Playoff Brier | Shield Brier | Points MAE | Points CRPS | Position MAE | Position RPS |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| Straight-line pace (`straight-line-pace-v1`) | 1.0549 | 0.1243 | 0.0509 | 4.540 | 3.261 | 1.583 | 0.1048 |
| Current pace (`current-pace-v1`) | 1.0505 | 0.1229 | 0.0509 | 4.538 | 3.267 | 1.579 | 0.1043 |
| Results Poisson (`results-poisson-v1`) | 1.0443 | 0.1233 | 0.0515 | 4.496 | 3.229 | 1.541 | 0.1017 |
| xG Poisson (`xg-poisson-v1`) | 1.0332 | 0.1145 | 0.0458 | 4.370 | 3.110 | 1.494 | 0.0961 |

## Paired final-test comparisons

Differences are candidate minus incumbent; negative values favor the candidate.

| Candidate | Metric | Difference | 95% interval | Date blocks |
| --- | --- | ---: | ---: | ---: |
| `straight-line-pace-v1` | match_log_loss | -0.0054 | [-0.0252, +0.0137] | 296 |
| `straight-line-pace-v1` | playoff_brier | -0.0019 | [-0.0036, -0.0004] | 296 |
| `straight-line-pace-v1` | shield_brier | +0.0003 | [-0.0010, +0.0018] | 296 |
| `straight-line-pace-v1` | points_crps | -0.1059 | [-0.1305, -0.0812] | 296 |
| `straight-line-pace-v1` | position_rps | -0.0024 | [-0.0031, -0.0017] | 296 |
| `current-pace-v1` | match_log_loss | -0.0053 | [-0.0179, +0.0070] | 296 |
| `current-pace-v1` | playoff_brier | -0.0025 | [-0.0042, -0.0008] | 296 |
| `current-pace-v1` | shield_brier | -0.0007 | [-0.0019, +0.0006] | 296 |
| `current-pace-v1` | points_crps | -0.1092 | [-0.1339, -0.0850] | 296 |
| `current-pace-v1` | position_rps | -0.0027 | [-0.0034, -0.0019] | 296 |
| `xg-poisson-v1` | match_log_loss | -0.0151 | [-0.0330, +0.0021] | 296 |
| `xg-poisson-v1` | playoff_brier | -0.0055 | [-0.0085, -0.0027] | 296 |
| `xg-poisson-v1` | shield_brier | -0.0033 | [-0.0050, -0.0017] | 296 |
| `xg-poisson-v1` | points_crps | -0.2055 | [-0.2523, -0.1598] | 296 |
| `xg-poisson-v1` | position_rps | -0.0079 | [-0.0094, -0.0064] | 296 |

The JSON artifact is the machine-readable source for all development/final-test stage buckets and fixed-decile calibration tables.

## Selection

No candidate met the precommitted replacement rule, so `results-poisson-v1` remains selected.

- `straight-line-pace-v1` did not qualify: evaluation-only reference model; excluded from selection.
- `current-pace-v1` did not qualify: final-test log-loss bootstrap interval was not entirely below zero.
- `xg-poisson-v1` did not qualify: final-test log-loss bootstrap interval was not entirely below zero.

## Limitations

- Historical ASA xG contains currently published or corrected values, not a reconstruction of when each value was first available.
- Daily UTC cutoffs prevent games on the same date from training one another.
