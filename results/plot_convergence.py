#!/usr/bin/env python3
"""Generate convergence plots from benchmark time-series CSV files."""

import csv
import os
import sys
import matplotlib
matplotlib.use('Agg')
import matplotlib.pyplot as plt
import numpy as np

TIMESERIES_DIR = os.path.join(os.path.dirname(__file__), 'timeseries')
OUTPUT_DIR = os.path.dirname(__file__)

POLICY_STYLES = {
    'epsilon-greedy':   {'color': '#e41a1c', 'marker': 'o', 'label': r'$\varepsilon$-Greedy'},
    'decaying-epsilon': {'color': '#377eb8', 'marker': 's', 'label': r'Decaying-$\varepsilon$'},
    'ucb1':             {'color': '#4daf4a', 'marker': '^', 'label': 'UCB1'},
    'thompson':         {'color': '#984ea3', 'marker': 'D', 'label': 'Thompson'},
    'contextual':       {'color': '#ff7f00', 'marker': 'v', 'label': 'LinUCB'},
    'latency':          {'color': '#a65628', 'marker': 'x', 'label': 'Min-Latency'},
    'random':           {'color': '#999999', 'marker': '+', 'label': 'Random'},
}

def load_timeseries(policy, nodes):
    fname = f'timeseries_{policy}_{nodes}.csv'
    path = os.path.join(TIMESERIES_DIR, fname)
    if not os.path.exists(path):
        return None, None
    indices, latencies = [], []
    with open(path) as f:
        reader = csv.DictReader(f)
        for row in reader:
            indices.append(int(row['request_index']))
            latencies.append(float(row['latency_ms']))
    return indices, latencies

def cumulative_avg(latencies):
    cum = np.cumsum(latencies)
    return cum / np.arange(1, len(cum) + 1)

def plot_convergence(nodes, output_name):
    fig, (ax1, ax2) = plt.subplots(1, 2, figsize=(12, 5))

    for policy, style in POLICY_STYLES.items():
        indices, latencies = load_timeseries(policy, nodes)
        if indices is None:
            continue

        # Left: raw per-request latency
        ax1.plot(indices, latencies, color=style['color'], marker=style['marker'],
                 markersize=5, linewidth=1, alpha=0.7, label=style['label'])

        # Right: cumulative average (convergence)
        cum_avg = cumulative_avg(latencies)
        ax2.plot(indices, cum_avg, color=style['color'], marker=style['marker'],
                 markersize=5, linewidth=1.5, label=style['label'])

    ax1.set_xlabel('Request Index')
    ax1.set_ylabel('Latency (ms)')
    ax1.set_title(f'Per-Request Latency (N={nodes})')
    ax1.legend(fontsize=8, loc='upper right')
    ax1.grid(True, alpha=0.3)

    ax2.set_xlabel('Request Index')
    ax2.set_ylabel('Cumulative Avg Latency (ms)')
    ax2.set_title(f'Policy Convergence (N={nodes})')
    ax2.legend(fontsize=8, loc='upper right')
    ax2.grid(True, alpha=0.3)

    plt.tight_layout()
    outpath = os.path.join(OUTPUT_DIR, output_name)
    plt.savefig(outpath, dpi=150, bbox_inches='tight')
    plt.close()
    print(f'Saved: {outpath}')

def plot_comparison_bar(csv_path, nodes, output_name):
    """Bar chart of avg latency and throughput from comparison CSV."""
    if not os.path.exists(csv_path):
        print(f'Skipping bar chart: {csv_path} not found')
        return

    policies, avg_lat, throughput, p95 = [], [], [], []
    with open(csv_path) as f:
        reader = csv.DictReader(f)
        for row in reader:
            pol = row['policy']
            if pol in POLICY_STYLES:
                policies.append(POLICY_STYLES[pol]['label'])
            else:
                policies.append(pol)
            avg_lat.append(float(row['avg_latency_ms']))
            throughput.append(float(row['throughput_mbs']))
            p95.append(float(row['p95_latency_ms']))

    fig, (ax1, ax2) = plt.subplots(1, 2, figsize=(12, 5))

    colors = [POLICY_STYLES.get(p, {}).get('color', '#333333')
              for p in list(POLICY_STYLES.keys())[:len(policies)]]

    x = np.arange(len(policies))
    width = 0.35

    # Latency comparison
    bars1 = ax1.bar(x - width/2, avg_lat, width, label='Avg', color=colors, alpha=0.8)
    bars2 = ax1.bar(x + width/2, p95, width, label='P95', color=colors, alpha=0.4)
    ax1.set_ylabel('Latency (ms)')
    ax1.set_title(f'Latency Comparison (N={nodes})')
    ax1.set_xticks(x)
    ax1.set_xticklabels(policies, rotation=30, ha='right', fontsize=8)
    ax1.legend()
    ax1.grid(True, alpha=0.3, axis='y')

    # Throughput comparison
    ax2.bar(x, throughput, color=colors, alpha=0.8)
    ax2.set_ylabel('Throughput (MB/s)')
    ax2.set_title(f'Throughput Comparison (N={nodes})')
    ax2.set_xticks(x)
    ax2.set_xticklabels(policies, rotation=30, ha='right', fontsize=8)
    ax2.grid(True, alpha=0.3, axis='y')

    plt.tight_layout()
    outpath = os.path.join(OUTPUT_DIR, output_name)
    plt.savefig(outpath, dpi=150, bbox_inches='tight')
    plt.close()
    print(f'Saved: {outpath}')

def plot_ablation(csv_path, output_name):
    """Bar chart of ablation study results."""
    if not os.path.exists(csv_path):
        print(f'Skipping ablation plot: {csv_path} not found')
        return

    configs, avg_lat, p95, cache_pct, delta = [], [], [], [], []
    ci_lower, ci_upper = [], []
    with open(csv_path) as f:
        reader = csv.DictReader(f)
        for row in reader:
            configs.append(row['configuration'])
            avg_lat.append(float(row['avg_latency_ms']))
            p95.append(float(row['p95_latency_ms']))
            cache_pct.append(float(row['cache_hit_ratio']) * 100)
            delta.append(float(row['delta_avg_pct']))
            ci_lower.append(float(row.get('ci95_lower', 0)))
            ci_upper.append(float(row.get('ci95_upper', 0)))

    fig, (ax1, ax2) = plt.subplots(1, 2, figsize=(13, 5))

    x = np.arange(len(configs))
    colors = ['#2ca02c', '#d62728', '#ff7f0e', '#9467bd', '#8c564b'][:len(configs)]

    # CI error bars
    ci_err = [(avg_lat[i] - ci_lower[i], ci_upper[i] - avg_lat[i]) for i in range(len(configs))]
    ci_err_lo = [e[0] for e in ci_err]
    ci_err_hi = [e[1] for e in ci_err]

    ax1.bar(x, avg_lat, color=colors, alpha=0.8, yerr=[ci_err_lo, ci_err_hi], capsize=4)
    ax1.set_ylabel('Avg Latency (ms)')
    ax1.set_title('Ablation: Avg Latency with 95% CI')
    ax1.set_xticks(x)
    ax1.set_xticklabels(configs, rotation=25, ha='right', fontsize=7)
    ax1.grid(True, alpha=0.3, axis='y')

    # Delta % chart
    bar_colors = ['#2ca02c' if d <= 0 else '#d62728' for d in delta]
    ax2.bar(x, delta, color=bar_colors, alpha=0.8)
    ax2.axhline(y=0, color='black', linewidth=0.8)
    ax2.set_ylabel('Δ Avg Latency vs Full System (%)')
    ax2.set_title('Ablation: Impact of Disabling Subsystems')
    ax2.set_xticks(x)
    ax2.set_xticklabels(configs, rotation=25, ha='right', fontsize=7)
    ax2.grid(True, alpha=0.3, axis='y')

    plt.tight_layout()
    outpath = os.path.join(OUTPUT_DIR, output_name)
    plt.savefig(outpath, dpi=150, bbox_inches='tight')
    plt.close()
    print(f'Saved: {outpath}')


def plot_fault_injection(csv_path, output_name):
    """Bar chart of fault injection results."""
    if not os.path.exists(csv_path):
        print(f'Skipping fault injection plot: {csv_path} not found')
        return

    scenarios, avg_lat, p95, errors, delta = [], [], [], [], []
    with open(csv_path) as f:
        reader = csv.DictReader(f)
        for row in reader:
            scenarios.append(row['scenario'])
            avg_lat.append(float(row['avg_latency_ms']))
            p95.append(float(row['p95_latency_ms']))
            errors.append(int(row['error_count']))
            delta.append(float(row['delta_avg_pct']))

    fig, (ax1, ax2) = plt.subplots(1, 2, figsize=(13, 5))

    x = np.arange(len(scenarios))
    colors = ['#2ca02c', '#d62728', '#ff7f0e', '#9467bd'][:len(scenarios)]

    width = 0.35
    ax1.bar(x - width/2, avg_lat, width, label='Avg', color=colors, alpha=0.8)
    ax1.bar(x + width/2, p95, width, label='P95', color=colors, alpha=0.4)
    ax1.set_ylabel('Latency (ms)')
    ax1.set_title('Fault Injection: Latency Impact')
    ax1.set_xticks(x)
    ax1.set_xticklabels(scenarios, rotation=25, ha='right', fontsize=7)
    ax1.legend()
    ax1.grid(True, alpha=0.3, axis='y')

    ax2.bar(x, errors, color=colors, alpha=0.8)
    ax2.set_ylabel('Avg Errors per Run')
    ax2.set_title('Fault Injection: Error Counts')
    ax2.set_xticks(x)
    ax2.set_xticklabels(scenarios, rotation=25, ha='right', fontsize=7)
    ax2.grid(True, alpha=0.3, axis='y')

    plt.tight_layout()
    outpath = os.path.join(OUTPUT_DIR, output_name)
    plt.savefig(outpath, dpi=150, bbox_inches='tight')
    plt.close()
    print(f'Saved: {outpath}')


if __name__ == '__main__':
    # Generate convergence plots for each available node count
    for n in [5, 10, 25]:
        # Check if any timeseries data exists for this node count
        test_file = os.path.join(TIMESERIES_DIR, f'timeseries_epsilon-greedy_{n}.csv')
        if os.path.exists(test_file):
            plot_convergence(n, f'convergence_n{n}.png')

    # Generate bar chart comparisons
    for n in [5, 10, 25]:
        csv_path = os.path.join(OUTPUT_DIR, f'compare_n{n}.csv')
        if os.path.exists(csv_path):
            plot_comparison_bar(csv_path, n, f'comparison_n{n}.png')

    # Generate ablation plot
    ablation_csv = os.path.join(OUTPUT_DIR, 'ablation.csv')
    plot_ablation(ablation_csv, 'ablation.png')

    # Generate fault injection plot
    fault_csv = os.path.join(OUTPUT_DIR, 'fault_injection.csv')
    plot_fault_injection(fault_csv, 'fault_injection.png')

    print('Done.')
