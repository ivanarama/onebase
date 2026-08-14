# Защита тегов релиза

Правила разделены на два ruleset-а, потому что их bypass-права различаются:

- `v*` может создавать только администратор репозитория;
- `build-*` может создавать администратор и GitHub Actions, которому это нужно
  для автоматической публикации сборок.

Применить оба ruleset-а через GitHub CLI:

```console
gh api -X POST repos/ivanarama/onebase/rulesets --input .github/tag-protection.json
gh api -X POST repos/ivanarama/onebase/rulesets --input .github/build-tag-protection.json
```

Перед повторным применением найдите уже созданные ruleset-ы и обновите их через
`PUT repos/ivanarama/onebase/rulesets/{ruleset_id}`, чтобы не создавать дубликаты.
Если прежний единый `release-tags` уже применён, сначала обновите его содержимым
`tag-protection.json` (он станет `release-v-tags`), затем создайте отдельный
`release-build-tags` из второго файла.
