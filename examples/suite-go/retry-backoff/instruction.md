`TestDelay` fails for large attempt numbers. `Delay` doubles the wait for
every retry and caps it at max, but a caller that has been retrying for a
long time passes a large attempt number, and the doubling silently wraps
round: attempt 62 comes back as zero instead of max.

Make `Delay` in `backoff.go` return max for every attempt whose undoubled
value would be at or above max, however large the attempt number is, while
the early attempts keep the delays they have now. The result must never be
negative and never below base. Do not change the test.
