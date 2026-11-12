import type { Feature, Me } from '../../src/types'

// GET /api/v1/me: a signed-in owner (the sign-in check answers 401 instead).
export const me: Me = {
  username: 'jo',
  roles: ['owner'],
  authMode: 'oidc',
  renewing: true,
  idleUntil: '2030-01-08T09:00:00Z',
}

// GET /api/v1/features: every section, with one-off researches allowed.
export const features: { features: Feature[] } = {
  features: [
    { name: 'factory' },
    { name: 'research', write: true, namespace: 'research' },
    { name: 'history' },
    { name: 'chat' },
  ],
}
