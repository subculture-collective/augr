DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM users WHERE username = 'demo')
       AND EXISTS (SELECT 1 FROM users WHERE username = 'patrick@subcult.tv') THEN
        RAISE EXCEPTION 'cannot rename demo user: target username already exists';
    END IF;

    UPDATE users
       SET username = 'patrick@subcult.tv',
           updated_at = NOW()
     WHERE username = 'demo';
END
$$;
