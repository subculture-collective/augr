DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM users WHERE username = 'patrick@subcult.tv')
       AND EXISTS (SELECT 1 FROM users WHERE username = 'demo') THEN
        RAISE EXCEPTION 'cannot restore demo user: target username already exists';
    END IF;

    UPDATE users
       SET username = 'demo',
           updated_at = NOW()
     WHERE username = 'patrick@subcult.tv';
END
$$;
