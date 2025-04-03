// --- START OF REVISED FILE commitlint.config.js ---
// Configuration for commitlint, used by CI (e.g., wagoid/commitlint-github-action)
// Enforces Conventional Commits standard. Ref: cicd.md#7.5
// See: https://commitlint.js.org/#/reference-configuration
// Based on @commitlint/config-conventional
module.exports = {
  extends: ["@commitlint/config-conventional"],
  // Add any project-specific rules or overrides here if needed
  // rules: {
  //   'type-enum': [2, 'always', ['feat', 'fix', 'docs', 'style', 'refactor', 'perf', 'test', 'build', 'ci', 'chore', 'revert']],
  //   'scope-case': [2, 'always', 'lower-case'],
  //   'subject-case': [2, 'never', ['sentence-case', 'start-case', 'pascal-case', 'upper-case']],
  //   'subject-empty': [2, 'never'],
  //   'subject-full-stop': [2, 'never', '.'],
  //   'header-max-length': [2, 'always', 72],
  //   'body-leading-blank': [1, 'always'],
  //   'footer-leading-blank': [1, 'always'],
  // }
};
// --- END OF REVISED FILE commitlint.config.js ---
