DELETE FROM iam_bindings WHERE principal LIKE 'group:%' OR role LIKE 'organizations/%' OR role LIKE 'projects/%';
DROP TABLE IF EXISTS iam_group_members;
DROP TABLE IF EXISTS iam_groups;
DROP TABLE IF EXISTS iam_custom_roles;
