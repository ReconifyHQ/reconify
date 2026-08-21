The bank (right.csv) deducts a 2.00 minor currency unit fee from our 100.00 payments (left.csv). Configure `reconify.yaml` to match these files by reference with a tolerance of up to 200 minor units (2.00).

Columns are: date, amount, currency, reference, description. Date layout is '2006-01-02'. Note: reconify amount parser uses multiplier: 100 by default so amounts are read as minor units.