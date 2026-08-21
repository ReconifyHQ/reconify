Our ledger (left.csv) has a single invoice of 100.00. The customer paid this in two installments of 50.00 (right.csv), both carrying the same invoice reference. Configure `reconify.yaml` with a `one_to_many` pass to group the right installments against the single left invoice.

Columns are: date, amount, currency, reference, description. Date layout is '2006-01-02'.