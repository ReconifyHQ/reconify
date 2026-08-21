The bank statement (right.csv) doesn't have a clean reference column (it is empty), but the invoice number (INV-008) is buried in the description (name column). Configure the pair to use `name_mode: tokens` so it can find the invoice number inside the name.

Columns are: date, amount, currency, reference, description. Date layout is '2006-01-02'.