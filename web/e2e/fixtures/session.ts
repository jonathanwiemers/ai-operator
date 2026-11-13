import type { About, Feature, Me } from '../../src/types'

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

// GET /api/v1/about: running 2026.11.3, a newer release being built.
export const about: About = {
  version: '2026.11.3',
  releasesURL: 'https://gitlab.example.com/team-a/ai-operator/-/releases',
  release: { version: '2026.11.4', state: 'Running', url: '' },
}
