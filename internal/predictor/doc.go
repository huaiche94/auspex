// Package predictor holds the deterministic, explainable prediction
// primitives (ADD §15, §16) that sit above internal/features. Day-one
// output is a risk score and quantile estimate, never a calibrated
// probability, per the predictor cold-start contract and Constitution §7
// rule 7 ("Uncalibrated risk scores are never labeled as probabilities").
package predictor
