import js from '@eslint/js'
import globals from 'globals'
import reactHooks from 'eslint-plugin-react-hooks'
import reactRefresh from 'eslint-plugin-react-refresh'
import tseslint from 'typescript-eslint'
import { defineConfig, globalIgnores } from 'eslint/config'

export default defineConfig([
  globalIgnores(['dist', 'public/mockServiceWorker.js']),
  {
    files: ['**/*.test.{ts,tsx}'],
    rules: {
      'no-restricted-properties': ['error', ...['it', 'test', 'describe'].flatMap((object) =>
        ['only', 'skip', 'todo', 'skipIf', 'runIf'].map((property) => ({ object, property,
          message: 'Frontend contracts must run in CI; express browser prerequisites through an explicit test project.' })))],
    },
  },
  {
    files: ['**/*.{ts,tsx}'],
    extends: [
      js.configs.recommended,
      tseslint.configs.recommended,
      reactHooks.configs['recommended-latest'],
      reactRefresh.configs.vite,
    ],
    languageOptions: {
      ecmaVersion: 2020,
      globals: globals.browser,
    },
  },
])
