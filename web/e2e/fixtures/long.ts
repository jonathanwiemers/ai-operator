// Long data for the overflow checks (#622): a milestone title whose last word
// has no break, a branch name, a GitLab URL, a code token and a project name.
// Each is longer than a phone's line, so a column, card or banner that does
// not wrap them scrolls the page sideways and fails the check.

// longTitle is about 120 characters; its last word has no hyphen or space.
export const longTitle = 'Move the order export and the refund ledger to the new reporting pipeline: OrderExportReconciliationForQuarterlyClose'

// longBranch is about 90 characters, as an agent's branch of a long issue title.
export const longBranch = 'feature/very-long-branch-name-for-refunds-of-orders-paid-in-a-foreign-currency-with-captures'

// longProject is a project name longer than a phone's line.
export const longProject = 'the-payments-platform-checkout-and-refunds-service-with-a-very-long-project-name'

// longURL is about 150 characters, a GitLab job URL in a deep group.
export const longURL =
  'https://gitlab.example.com/team-a/platform/payments/checkout-and-refunds/shop-payments-worker/-/jobs/8820145/artifacts/browse/reports/coverage/index.html'

// longCode is about 80 characters, an image reference quoted in a pause reason.
export const longCode = 'registry.gitlab.example.com/team-a/shop/payments-worker:sha-a1b2c3d4e5f6a7b8c9d0e1f2'
