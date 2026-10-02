"""
Synthetic transaction dataset generator for training the Fraud ML Scorer.
Generates realistic behavioural patterns with natural class imbalance (~0.5% fraud)
and labelled attack scenarios (bursts, card-testing, account takeover, impossible travel).
"""

import math
import random
import uuid
from datetime import datetime, timedelta
import pandas as pd
import numpy as np

def generate_dataset(n_txns=20000, seed=42):
    np.random.seed(seed)
    random.seed(seed)

    n_accounts = 250
    accounts = [str(uuid.uuid4()) for _ in range(n_accounts)]
    account_means = {acct: math.log(random.randint(1000, 3500)) for acct in accounts}
    account_home_country = {acct: "GB" for acct in accounts}
    merchants = ["Tesco", "Sainsburys", "Amazon UK", "Uber", "Deliveroo", "Costa Coffee", "Shell", "Marks & Spencer"]

    base_time = datetime(2026, 9, 1, 8, 0, 0)
    data = []

    # 1. Generate legitimate baseline
    for i in range(n_txns):
        acct = random.choice(accounts)
        mean_log = account_means[acct]
        amt = int(max(200, math.exp(mean_log + np.random.normal(0, 0.45))))
        t_delta = timedelta(minutes=i * 2 + random.randint(0, 60))
        occurred_at = base_time + t_delta

        merch = random.choice(merchants)
        channel = random.choice(["contactless", "chip", "online", "atm"])
        hour = occurred_at.hour

        data.append({
            "account_id": acct,
            "occurred_at": occurred_at,
            "amount_minor": amt,
            "amount_to_mean": amt / math.exp(mean_log),
            "z_score": (math.log(amt) - mean_log) / 0.45,
            "count_1m": 0,
            "count_10m": random.choice([0, 1]),
            "sum_1h": amt + random.randint(0, 1500),
            "is_new_country": 0,
            "is_new_merchant": 0 if random.random() > 0.15 else 1,
            "hour_sin": math.sin(2 * math.pi * hour / 24.0),
            "hour_cos": math.cos(2 * math.pi * hour / 24.0),
            "channel": channel,
            "secs_since_last_txn": float(random.randint(300, 86400)),
            "account_age_days": float(random.randint(15, 365)),
            "is_fraud": 0,
            "scenario": "normal"
        })

    # 2. Inject labelled fraud attacks (~0.5% fraud rate)
    # A) Velocity bursts
    for _ in range(8):
        victim = random.choice(accounts)
        t_start = base_time + timedelta(minutes=random.randint(1000, 30000))
        for step in range(6):
            data.append({
                "account_id": victim,
                "occurred_at": t_start + timedelta(seconds=step * 7),
                "amount_minor": 4200,
                "amount_to_mean": 4200 / math.exp(account_means[victim]),
                "z_score": (math.log(4200) - account_means[victim]) / 0.45,
                "count_1m": step,
                "count_10m": step + 1,
                "sum_1h": 4200 * (step + 1),
                "is_new_country": 0,
                "is_new_merchant": 1,
                "hour_sin": math.sin(2 * math.pi * t_start.hour / 24.0),
                "hour_cos": math.cos(2 * math.pi * t_start.hour / 24.0),
                "channel": "online",
                "secs_since_last_txn": 7.0,
                "account_age_days": 45.0,
                "is_fraud": 1,
                "scenario": "velocity_burst"
            })

    # B) Card testing sequences
    for _ in range(10):
        victim = random.choice(accounts)
        t_start = base_time + timedelta(minutes=random.randint(2000, 35000))
        for step in range(4):
            data.append({
                "account_id": victim,
                "occurred_at": t_start + timedelta(seconds=step * 30),
                "amount_minor": 120,
                "amount_to_mean": 120 / math.exp(account_means[victim]),
                "z_score": (math.log(120) - account_means[victim]) / 0.45,
                "count_1m": 0,
                "count_10m": step,
                "sum_1h": 120 * (step + 1),
                "is_new_country": 0,
                "is_new_merchant": 1,
                "hour_sin": math.sin(2 * math.pi * t_start.hour / 24.0),
                "hour_cos": math.cos(2 * math.pi * t_start.hour / 24.0),
                "channel": "online",
                "secs_since_last_txn": 30.0,
                "account_age_days": 80.0,
                "is_fraud": 1,
                "scenario": "card_testing"
            })
        # Cashout transaction
        data.append({
            "account_id": victim,
            "occurred_at": t_start + timedelta(minutes=3),
            "amount_minor": 9800,
            "amount_to_mean": 9800 / math.exp(account_means[victim]),
            "z_score": (math.log(9800) - account_means[victim]) / 0.45,
            "count_1m": 0,
            "count_10m": 4,
            "sum_1h": 10280,
            "is_new_country": 0,
            "is_new_merchant": 1,
            "hour_sin": math.sin(2 * math.pi * t_start.hour / 24.0),
            "hour_cos": math.cos(2 * math.pi * t_start.hour / 24.0),
            "channel": "online",
            "secs_since_last_txn": 120.0,
            "account_age_days": 80.0,
            "is_fraud": 1,
            "scenario": "card_testing"
        })

    # C) Account takeover: new country + high value online
    for _ in range(12):
        victim = random.choice(accounts)
        t_start = base_time + timedelta(minutes=random.randint(3000, 38000))
        data.append({
            "account_id": victim,
            "occurred_at": t_start,
            "amount_minor": 45000,
            "amount_to_mean": 45000 / math.exp(account_means[victim]),
            "z_score": (math.log(45000) - account_means[victim]) / 0.45,
            "count_1m": 0,
            "count_10m": 0,
            "sum_1h": 45000,
            "is_new_country": 1,
            "is_new_merchant": 1,
            "hour_sin": math.sin(2 * math.pi * t_start.hour / 24.0),
            "hour_cos": math.cos(2 * math.pi * t_start.hour / 24.0),
            "channel": "online",
            "secs_since_last_txn": float(random.randint(1800, 7200)),
            "account_age_days": float(random.randint(30, 200)),
            "is_fraud": 1,
            "scenario": "account_takeover"
        })

    df = pd.DataFrame(data)
    df.sort_values(by="occurred_at", inplace=True)
    df.reset_index(drop=True, inplace=True)
    return df

if __name__ == "__main__":
    df = generate_dataset()
    print(f"Generated {len(df)} transactions with {df['is_fraud'].sum()} fraud records ({df['is_fraud'].mean()*100:.2f}%)")
    df.to_csv("ml/synthetic_transactions.csv", index=False)
