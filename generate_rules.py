import os

rules = [
    ("go-logs", "go", "any:\n    - pattern: $LOGGER.Info($FORMAT)\n    - pattern: $LOGGER.Debug($FORMAT)\n    - pattern: $LOGGER.Warn($FORMAT)\n    - pattern: $LOGGER.Error($FORMAT)\n    - pattern: $LOGGER.Fatal($FORMAT)\n    - pattern: $LOGGER.Print($FORMAT)"),
    ("go-chunk", "go", "any:\n    - kind: function_declaration\n    - kind: method_declaration\n    - kind: type_declaration"),
    
    ("js-logs", "javascript", "any:\n    - pattern: $LOGGER.log($FORMAT)\n    - pattern: $LOGGER.info($FORMAT)\n    - pattern: $LOGGER.warn($FORMAT)\n    - pattern: $LOGGER.error($FORMAT)\n    - pattern: $LOGGER.debug($FORMAT)"),
    ("js-chunk", "javascript", "any:\n    - kind: function_declaration\n    - kind: method_definition\n    - kind: class_declaration"),
    
    ("ts-logs", "typescript", "any:\n    - pattern: $LOGGER.log($FORMAT)\n    - pattern: $LOGGER.info($FORMAT)\n    - pattern: $LOGGER.warn($FORMAT)\n    - pattern: $LOGGER.error($FORMAT)\n    - pattern: $LOGGER.debug($FORMAT)"),
    ("ts-chunk", "typescript", "any:\n    - kind: function_declaration\n    - kind: method_definition\n    - kind: class_declaration\n    - kind: interface_declaration"),
    
    ("java-logs", "java", "any:\n    - pattern: $LOGGER.info($FORMAT)\n    - pattern: $LOGGER.debug($FORMAT)\n    - pattern: $LOGGER.warn($FORMAT)\n    - pattern: $LOGGER.error($FORMAT)\n    - pattern: $LOGGER.trace($FORMAT)"),
    ("java-chunk", "java", "any:\n    - kind: method_declaration\n    - kind: class_declaration"),
    
    ("py-logs", "python", "any:\n    - pattern: $LOGGER.info($FORMAT)\n    - pattern: $LOGGER.debug($FORMAT)\n    - pattern: $LOGGER.warning($FORMAT)\n    - pattern: $LOGGER.error($FORMAT)\n    - pattern: $LOGGER.critical($FORMAT)"),
    ("py-chunk", "python", "any:\n    - kind: function_definition\n    - kind: class_definition"),
    
    ("cpp-logs", "cpp", "any:\n    - pattern: printf($FORMAT, $$$ARGS)\n    - pattern: $LOGGER.info($FORMAT)\n    - pattern: $LOGGER.error($FORMAT)"),
    ("cpp-chunk", "cpp", "any:\n    - kind: function_definition\n    - kind: class_specifier"),
]

os.makedirs('rules', exist_ok=True)
for id_name, lang, rule in rules:
    with open(f"rules/{id_name}.yml", "w") as f:
        f.write(f"id: {id_name}\nlanguage: {lang}\nrule:\n  {rule}\n")
