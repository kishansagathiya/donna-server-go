-- Purge junk identity facts from naive "I'm …" name extraction.
-- Keeps real names containing "Kishan" (dogfood); deactivates other "User's name is …" junk.

update kb_facts
set active = false
where active = true
  and fact ilike 'User''s name is %'
  and fact !~* 'name is Kishan';

update kb_user_profiles
set identity_facts = coalesce((
  select array_agg(elem order by ord)
  from unnest(identity_facts) with ordinality as t(elem, ord)
  where elem !~* 'name is '
     or elem ~* 'name is Kishan'
), '{}'::text[]),
    updated_at = now()
where identity_facts is not null
  and cardinality(identity_facts) > 0
  and exists (
    select 1
    from unnest(identity_facts) as elem
    where elem ~* 'name is '
      and elem !~* 'name is Kishan'
  );
