"""
FastAPI ML Scorer Sidecar.
Provides low-latency scoring (<5ms) for the fraud evaluation pipeline.
"""

import os
import joblib
import numpy as np
from fastapi import FastAPI, HTTPException
from pydantic import BaseModel, Field

app = FastAPI(title="Fraud ML Scorer Sidecar", version="1.0.0")

MODEL_PATH = os.getenv("MODEL_PATH", "ml/model.joblib")
model_artifact = None

class FeatureVector(BaseModel):
    amount_minor: int = 0
    amount_to_mean: float = 1.0
    z_score: float = 0.0
    count_1m: int = 0
    count_10m: int = 0
    sum_1h: int = 0
    is_new_country: int = 0
    is_new_merchant: int = 0
    hour_sin: float = 0.0
    hour_cos: float = 1.0
    channel: str = "contactless"
    secs_since_last_txn: float = -1.0
    account_age_days: float = 0.0

class ScoreRequest(BaseModel):
    features: FeatureVector

class ScoreResponse(BaseModel):
    score: float
    model_version: str

@app.on_event("startup")
def load_model():
    global model_artifact
    if os.path.exists(MODEL_PATH):
        try:
            model_artifact = joblib.load(MODEL_PATH)
            print(f"Loaded model artifact from {MODEL_PATH} (version: {model_artifact.get('version')})")
        except Exception as e:
            print(f"Warning: Could not load model artifact: {e}")
    else:
        print(f"Model file {MODEL_PATH} not found yet; will use fallback heuristic model.")

@app.get("/health")
def health_check():
    return {
        "status": "ok",
        "model_loaded": model_artifact is not None,
        "model_version": model_artifact.get("version", "heuristic-v1") if model_artifact else "heuristic-v1"
    }

@app.post("/score", response_model=ScoreResponse)
def score_features(req: ScoreRequest):
    f = req.features

    if model_artifact is not None:
        try:
            model = model_artifact["model"]
            cols = model_artifact["feature_cols"]
            version = model_artifact.get("version", "lgbm-2026-09-28")

            # Extract features according to feature_cols
            ch = f.channel.lower()
            feat_dict = {
                "amount_to_mean": f.amount_to_mean,
                "z_score": f.z_score,
                "count_1m": f.count_1m,
                "count_10m": f.count_10m,
                "sum_1h": f.sum_1h,
                "is_new_country": f.is_new_country,
                "is_new_merchant": f.is_new_merchant,
                "hour_sin": f.hour_sin,
                "hour_cos": f.hour_cos,
                "channel_contactless": 1.0 if ch == "contactless" else 0.0,
                "channel_chip": 1.0 if ch == "chip" else 0.0,
                "channel_online": 1.0 if ch == "online" else 0.0,
                "channel_atm": 1.0 if ch == "atm" else 0.0,
                "secs_since_last_txn": f.secs_since_last_txn,
                "account_age_days": f.account_age_days,
            }

            row = [feat_dict.get(c, 0.0) for c in cols]
            X = np.array([row])

            # Output calibrated probability of class 1 (fraud)
            proba = float(model.predict_proba(X)[0][1])
            # Bound score to [0, 1]
            proba = max(0.0, min(1.0, proba))
            return ScoreResponse(score=proba, model_version=version)
        except Exception as e:
            raise HTTPException(status_code=500, detail=f"Inference error: {str(e)}")

    # Fallback heuristic scorer when model.joblib is not present
    heuristic_score = 0.05
    if f.count_1m >= 5:
        heuristic_score += 0.45
    if f.z_score > 3.5:
        heuristic_score += 0.35
    if f.is_new_country == 1:
        heuristic_score += 0.30
    if f.is_new_merchant == 1 and f.amount_to_mean > 5.0:
        heuristic_score += 0.20

    score = min(0.99, heuristic_score)
    return ScoreResponse(score=score, model_version="heuristic-fallback-v1")

if __name__ == "__main__":
    import uvicorn
    uvicorn.run(app, host="0.0.0.0", port=8000)
