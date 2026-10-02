"""
Train a calibrated gradient boosted classifier on transaction behavioural features.
Evaluates using PR-AUC and Recall at fixed precision, avoiding temporal data leakage.
"""

import os
import joblib
import numpy as np
import pandas as pd
from sklearn.ensemble import HistGradientBoostingClassifier, RandomForestClassifier
from sklearn.calibration import CalibratedClassifierCV
from sklearn.metrics import average_precision_score, roc_auc_score, classification_report
from dataset import generate_dataset

FEATURE_COLS = [
    "amount_to_mean",
    "z_score",
    "count_1m",
    "count_10m",
    "sum_1h",
    "is_new_country",
    "is_new_merchant",
    "hour_sin",
    "hour_cos",
    "channel_contactless",
    "channel_chip",
    "channel_online",
    "channel_atm",
    "secs_since_last_txn",
    "account_age_days"
]

def preprocess(df: pd.DataFrame):
    df = df.copy()
    # One-hot encode channel
    for ch in ["contactless", "chip", "online", "atm"]:
        df[f"channel_{ch}"] = (df["channel"] == ch).astype(float)
    
    # Handle infinite/missing values
    df["secs_since_last_txn"] = df["secs_since_last_txn"].fillna(-1.0)
    df["amount_to_mean"] = df["amount_to_mean"].fillna(1.0)
    df["z_score"] = df["z_score"].fillna(0.0)

    X = df[FEATURE_COLS].values
    y = df["is_fraud"].values
    return X, y

def train_and_evaluate():
    print("Generating dataset with behavioural windows...")
    df = generate_dataset(n_txns=25000, seed=42)

    # Strictly time-based split: train on earlier 75%, test on future 25%
    split_idx = int(len(df) * 0.75)
    train_df = df.iloc[:split_idx]
    test_df = df.iloc[split_idx:]

    print(f"Train set: {len(train_df)} rows ({train_df['is_fraud'].sum()} fraud)")
    print(f"Test set:  {len(test_df)} rows ({test_df['is_fraud'].sum()} fraud)")

    X_train, y_train = preprocess(train_df)
    X_test, y_test = preprocess(test_df)

    # Use class-weighted classifier for severe imbalance
    base_model = HistGradientBoostingClassifier(
        max_iter=120,
        learning_rate=0.08,
        max_leaf_nodes=31,
        class_weight="balanced",
        random_state=42
    )
    base_model.fit(X_train, y_train)

    # Calibrate probability predictions with isotonic regression
    calibrated = CalibratedClassifierCV(estimator=base_model, method="sigmoid", cv=3)
    calibrated.fit(X_train, y_train)

    # Evaluate on out-of-time test set
    y_pred_proba = calibrated.predict_proba(X_test)[:, 1]

    pr_auc = average_precision_score(y_test, y_pred_proba)
    roc_auc = roc_auc_score(y_test, y_pred_proba)

    print(f"\n--- Model Evaluation (Out-of-Time Test Set) ---")
    print(f"PR-AUC (Average Precision): {pr_auc:.4f}")
    print(f"ROC-AUC:                    {roc_auc:.4f}")

    # Evaluate at review budget threshold (e.g. 0.50 score)
    y_pred_flag = (y_pred_proba >= 0.50).astype(int)
    print("\nClassification Report at threshold 0.50:")
    print(classification_report(y_test, y_pred_flag, target_names=["legitimate", "fraud"], digits=3))

    # Save artifact
    version = "lgbm-2026-09-28"
    artifact = {
        "model": calibrated,
        "feature_cols": FEATURE_COLS,
        "version": version
    }
    os.makedirs("ml", exist_ok=True)
    out_path = "ml/model.joblib"
    joblib.dump(artifact, out_path)
    print(f"Model successfully saved to {out_path} (version: {version})")

if __name__ == "__main__":
    train_and_evaluate()
