`TestOrder` fails. `Order` wants releases newest day first and, within one
day, by name ascending. The comparator it uses joins the two conditions
with a single "or", which is not an ordering at all: for two releases on
different days with names the other way round it reports *each* of them as
the earlier one, and a sort given a self-contradicting comparator produces
whatever it produces.

Write the comparison as the two levels it is — day first, and name only
when the days are equal — in `order.go`. Do not change the test.
