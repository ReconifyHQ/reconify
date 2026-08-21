We are reconciling our internal ledger (left.csv) against our bank statement (right.csv).
Write a `reconify.yaml` configuration to reconcile these two files.

The left and right files have the following columns:
- date
- amount
- currency
- reference
- description

Please configure the parser for both sides to use these columns. The date layout is "2006-01-02".
The amounts do not need any multiplier or special formatting.
