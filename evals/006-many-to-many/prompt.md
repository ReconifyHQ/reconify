Both the ledger (left.csv) and gateway (right.csv) have multiple rows for the same payout reference (PO-006) totaling 100.00. Configure a `many_to_many` pass to group and match them by their shared reference.

Columns are: date, amount, currency, reference, description. Date layout is '2006-01-02'.