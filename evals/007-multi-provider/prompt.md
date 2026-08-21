We reconcile our ledger (left.csv) against two payment processors: stripe.csv and paypal.csv. Configure three sources in `reconify.yaml` (left, stripe, paypal) and one pair using `rights: [stripe, paypal]`.

Columns for all files are: date, amount, currency, reference, description. Date layout is '2006-01-02'.