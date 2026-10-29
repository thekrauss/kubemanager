-- IAM phase A, étape A8 : les membres de projet sont des bindings IAM
-- (repository/members_iam.go). project_members n'est plus lue ni écrite.
DROP TABLE IF EXISTS project_members;
