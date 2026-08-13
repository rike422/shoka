; Definition-only tags for shoka (no @reference / predicates).
(class
  name: [
    (constant) @name
    (scope_resolution name: (_) @name)
  ]) @definition.class

(singleton_class
  value: [
    (constant) @name
    (scope_resolution name: (_) @name)
  ]) @definition.class

(module
  name: [
    (constant) @name
    (scope_resolution name: (_) @name)
  ]) @definition.module

(method
  name: (_) @name) @definition.method

(singleton_method
  name: (_) @name) @definition.method
